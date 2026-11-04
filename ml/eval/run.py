#!/usr/bin/env python3
"""Rehearse evaluation harness (Phase 4).

For every scenario x N runs: inject the fault, wait until the symptom is visible, ask the
agent (`rehearse why deploy/<ask> --json`), score the answer, then ALWAYS clean up and wait
until the app is healthy and its specs are exactly as before.

Scoring follows AIOpsLab's task levels (orchestrator/tasks/*, evaluators/quantitative.py):
detection, localization (target, any-of like is_subset), category and action, plus time,
model calls, tool calls and tokens.

Usage (from the repo root):
    ml/.venv/bin/python ml/eval/run.py --runs 3
    ml/.venv/bin/python ml/eval/run.py --only wrong-image,oom-low-limit --runs 1
    ml/.venv/bin/python ml/eval/run.py --label rag --extra-args=--rag      # later phases
Results are appended to ml/eval/results/<label>.csv (git-ignored); finished
(scenario, run) pairs are skipped on restart, so a long run can be resumed.
"""
from __future__ import annotations

import argparse
import atexit
import csv
import json
import os
import shutil
import signal
import subprocess
import sys
import time
from dataclasses import dataclass, field
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
SCENARIOS = ROOT / "scenarios"
SCENARIO_DIR = SCENARIOS  # set from --scenarios
RESULTS = ROOT / "ml" / "eval" / "results"
REHEARSE = ROOT / "bin" / "rehearse"

FIELDS = [
    "label", "scenario", "run", "fault", "ask", "detected", "target_ok", "category_ok", "action_ok",
    "correct", "false_alarm", "answer_target", "answer_category", "answer_action", "confidence",
    "valid", "triaged", "steps", "tool_calls", "prompt_tokens", "completion_tokens", "seconds",
    "inject_seconds", "error", "trace",
]


def log(msg: str) -> None:
    print(time.strftime("%H:%M:%S"), msg, flush=True)


# --------------------------------------------------------------------------- cluster helpers

@dataclass
class Env:
    context: str = "kind-rehearse"
    namespace: str = "hotel"
    dry_run: bool = False
    config: str = "config.yaml"
    health_service: str = "frontend"   # whose endpoint metrics show "users are fine"

    def kubectl(self, *args: str, check: bool = True, capture: bool = False) -> str:
        cmd = ["kubectl", "--context", self.context, *[a.replace("{ns}", self.namespace) for a in args]]
        return self._run(cmd, check=check, capture=capture)

    def make(self, target: str) -> None:
        self._run(["make", "-s", target], check=True)

    def rehearse(self, *args: str) -> dict | list:
        out = self._run([str(REHEARSE), "--config", self.config, "--context", self.context, "-n", self.namespace, *args],
                        check=True, capture=True)
        return json.loads(out) if out.strip() else {}

    def _run(self, cmd: list[str], check: bool, capture: bool = False) -> str:
        if self.dry_run:
            log("DRY " + " ".join(cmd))
            return "[]" if capture else ""
        p = subprocess.run(cmd, cwd=ROOT, text=True, capture_output=True, timeout=900)
        if check and p.returncode != 0:
            raise RuntimeError(f"{' '.join(cmd[:6])}... failed: {p.stderr.strip()[:400]}")
        return p.stdout


def run_steps(env: Env, steps: list[dict], cleanup: bool = False) -> None:
    """Run inject or cleanup steps. During cleanup, keep going after a failed step."""
    for step in steps or []:
        try:
            if "kubectl" in step:
                env.kubectl(*step["kubectl"])
            elif "make" in step:
                env.make(step["make"])
            elif "manifest" in step:
                env.kubectl("apply", "-f", str(SCENARIO_DIR / step["manifest"]))
            elif "manifest_delete" in step:
                env.kubectl("delete", "--ignore-not-found", "-f", str(SCENARIO_DIR / step["manifest_delete"]))
            elif "exec" in step:
                sel, script = step["exec"]["selector"], step["exec"]["script"]
                pod = env.kubectl("-n", "{ns}", "get", "pod", "-l", sel, "-o", "jsonpath={.items[0].metadata.name}",
                                  capture=True).strip()
                env.kubectl("-n", "{ns}", "exec", pod, "--", "/bin/bash", script)
            elif "sleep" in step:
                time.sleep(float(step["sleep"]))
            else:
                raise ValueError(f"unknown step {step}")
        except Exception as e:  # noqa: BLE001 - cleanup must try every step
            if not cleanup:
                raise
            log(f"  cleanup step failed (continuing): {e}")


def snapshot(env: Env) -> dict:
    """Specs of all Deployments and Services, to prove cleanup restored them exactly."""
    out = {}
    for kind in ("deployments", "services"):
        items = json.loads(env.kubectl("-n", "{ns}", "get", kind, "-o", "json", capture=True) or '{"items":[]}')["items"]
        for it in items:
            spec = it.get("spec", {})
            if kind == "services":
                spec = {k: v for k, v in spec.items() if k not in ("clusterIP", "clusterIPs")}
            else:
                spec.get("template", {}).get("metadata", {}).get("annotations", {}).pop(
                    "kubectl.kubernetes.io/restartedAt", None)
            out[f"{kind}/{it['metadata']['name']}"] = json.dumps(spec, sort_keys=True)
    return out


def wait_healthy(env: Env, timeout: float = 420) -> bool:
    """Healthy = `rehearse status` empty and no k6 errors in the last minute."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            issues = env.rehearse("status", "-o", "json")
            m = env.rehearse("metrics", env.health_service, "--window", "1m", "-o", "json")
            errors = max((e.get("error_ratio", 0) for e in (m.get("endpoints") or [])), default=0)
            if not issues and errors < 0.01:
                return True
        except Exception as e:  # noqa: BLE001
            log(f"  health check error: {e}")
        time.sleep(10)
    return False


def symptom_visible(env: Env, w: dict) -> bool:
    if "issue" in w:
        want = w["issue"] if isinstance(w["issue"], list) else [w["issue"]]
        if any(i.get("reason") in want for i in env.rehearse("status", "-o", "json") or []):
            return True
    for key, metric, threshold in (("error_endpoint", "error_ratio", w.get("min_error_ratio", 0.2)),
                                   ("p95_ms_endpoint", "p95_ms", w.get("min_p95_ms", 1000))):
        if key in w:
            m = env.rehearse("metrics", w.get("metrics_service", env.health_service), "--window", "1m", "-o", "json")
            for e in m.get("endpoints") or []:
                if e["endpoint"] == w[key] and e.get(metric, 0) >= threshold:
                    return True
    return False


def wait_symptom(env: Env, w: dict) -> bool:
    if env.dry_run:
        return True
    if set(w) <= {"seconds", "timeout"}:
        time.sleep(float(w.get("seconds", 30)))
        return True
    deadline = time.time() + float(w.get("timeout", 180))
    while time.time() < deadline:
        if symptom_visible(env, w):
            time.sleep(15)  # let a few more samples and log lines arrive
            return True
        time.sleep(10)
    return False


# --------------------------------------------------------------------------- scoring

def score(sc: dict, result: dict) -> dict:
    """Score one answer like AIOpsLab: detection, localization, category, action."""
    exp = sc["expected"]
    ans = result.get("answer") or {}
    action = (ans.get("action") or {}).get("type", "None")
    category = ans.get("category", "unknown")
    target = (ans.get("target") or "").strip()
    says_fault = action != "None" or category != "unknown"

    if exp["fault"]:
        detected = says_fault
        target_ok = target in exp["targets"]
        category_ok = category in exp["categories"]
        action_ok = action in exp["actions"]
        false_alarm = False
    else:
        detected = not says_fault          # "detected" = correctly said nothing is wrong
        target_ok = category_ok = action_ok = detected
        false_alarm = says_fault
    return {
        "detected": detected, "target_ok": target_ok, "category_ok": category_ok, "action_ok": action_ok,
        "correct": detected and target_ok and category_ok and action_ok, "false_alarm": false_alarm,
        "answer_target": target, "answer_category": category, "answer_action": action,
        "confidence": ans.get("confidence", 0),
    }


# --------------------------------------------------------------------------- main loop

@dataclass
class Cleanup:
    """Holds the cleanup of the scenario in flight; runs it on exit or Ctrl-C (like AIOpsLab's atexit)."""
    env: Env
    steps: list = field(default_factory=list)
    name: str = ""

    def run(self) -> None:
        if self.steps:
            steps, self.steps = self.steps, []
            log(f"  cleanup {self.name}")
            run_steps(self.env, steps, cleanup=True)


def load_scenarios(only: str | None, folder: Path = SCENARIOS) -> list[dict]:
    files = sorted(p for p in folder.glob("*.yaml"))
    scs = [yaml.safe_load(p.read_text()) for p in files]
    if only:
        names = set(only.split(","))
        scs = [s for s in scs if s["name"] in names]
        missing = names - {s["name"] for s in scs}
        if missing:
            sys.exit(f"unknown scenarios: {missing}")
    return scs


def done_pairs(path: Path) -> set[tuple[str, int]]:
    if not path.exists():
        return set()
    with path.open() as f:
        return {(r["scenario"], int(r["run"])) for r in csv.DictReader(f)}


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--runs", type=int, default=3, help="runs per scenario (models vary; default 3)")
    ap.add_argument("--only", help="comma-separated scenario names")
    ap.add_argument("--label", default="baseline", help="results file name, e.g. baseline, rag, tuned")
    ap.add_argument("--extra-args", default="", help="extra flags for `rehearse why`, e.g. --always-ask")
    ap.add_argument("--context", default="kind-rehearse")
    ap.add_argument("--namespace", default="hotel")
    ap.add_argument("--dry-run", action="store_true", help="print commands, change nothing")
    ap.add_argument("--scenarios", default=str(SCENARIOS), help="scenario folder (scenarios-app2 for the second app)")
    ap.add_argument("--config", default="config.yaml", help="rehearse config file")
    ap.add_argument("--health-service", default="frontend", help="service whose endpoints show user-facing health")
    ap.add_argument("--no-agent", action="store_true", help="only check that scenarios inject and clean up")
    args = ap.parse_args()

    global SCENARIO_DIR
    SCENARIO_DIR = Path(args.scenarios)
    env = Env(context=args.context, namespace=args.namespace, dry_run=args.dry_run,
              config=args.config, health_service=args.health_service)
    RESULTS.mkdir(parents=True, exist_ok=True)
    # Freeze the binary: rebuilding bin/rehearse during a multi-hour run must not change it.
    global REHEARSE
    frozen = RESULTS / ".bin" / f"rehearse-{args.label}"
    frozen.parent.mkdir(exist_ok=True)
    if not args.dry_run:
        shutil.copy2(REHEARSE, frozen)
        REHEARSE = frozen
    out_path = RESULTS / f"{args.label}.csv"
    done = done_pairs(out_path)
    scenarios = load_scenarios(args.only, SCENARIO_DIR)

    pending = Cleanup(env)
    atexit.register(pending.run)
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(130))  # SIGTERM -> atexit cleanup

    if not env.dry_run and not wait_healthy(env, timeout=300):
        log("app is not healthy before starting; fix it first (make status)")
        return 1

    new_file = not out_path.exists()
    with out_path.open("a", newline="") as f:
        w = csv.DictWriter(f, fieldnames=FIELDS)
        if new_file:
            w.writeheader()
        for run in range(1, args.runs + 1):
            for sc in scenarios:
                if (sc["name"], run) in done:
                    continue
                row = run_one(env, sc, run, args, pending)
                w.writerow(row)
                f.flush()
    summarize(out_path)
    return 0


def run_one(env: Env, sc: dict, run: int, args, pending: Cleanup) -> dict:
    log(f"== {sc['name']} run {run}")
    row = {k: "" for k in FIELDS}
    row.update(label=args.label, scenario=sc["name"], run=run, fault=sc["expected"]["fault"], ask=sc["ask"])
    before = snapshot(env) if not env.dry_run else {}
    t0 = time.time()
    try:
        pending.steps, pending.name = list(sc.get("cleanup") or []), sc["name"]
        run_steps(env, sc.get("inject") or [])
        if not wait_symptom(env, sc.get("wait_for") or {}):
            raise RuntimeError("symptom never became visible (scenario problem, not an agent problem)")
        row["inject_seconds"] = round(time.time() - t0)
        if args.no_agent:
            log(f"   symptom visible after {row['inject_seconds']}s")
            return row
        why = ["why", f"deploy/{sc['ask']}", "--json", *[a for a in args.extra_args.split() if a]]
        result = env.rehearse(*why) if not env.dry_run else {"answer": {}}
        row.update(score(sc, result))
        row.update(valid=result.get("valid"), triaged=result.get("triaged"), steps=result.get("steps"),
                   tool_calls=result.get("tool_calls"), prompt_tokens=result.get("prompt_tokens"),
                   completion_tokens=result.get("completion_tokens"),
                   seconds=round((result.get("duration_ns") or 0) / 1e9, 1),
                   error=result.get("error", ""), trace=result.get("trace_path", ""))
        log(f"   -> {row['answer_action']} on {row['answer_target']} ({row['answer_category']}), "
            f"correct={row['correct']} in {row['seconds']}s")
    except Exception as e:  # noqa: BLE001
        row["error"] = f"harness: {e}"
        log(f"   !! {e}")
    finally:
        pending.run()
        healthy = wait_healthy(env) if not env.dry_run else True
        after = snapshot(env) if not env.dry_run else {}
        changed = sorted(k for k in set(before) | set(after) if before.get(k) != after.get(k))
        if not healthy or changed:
            msg = f"NOT RESTORED: healthy={healthy} changed={changed}"
            log("   !! " + msg)
            row["error"] = (row["error"] + "; " if row["error"] else "") + msg
            if changed:
                env.kubectl("apply", "-k", "deploy/hotel", check=False)
    return row


def summarize(path: Path) -> None:
    """Print a per-scenario table and the totals (also used by summary.py)."""
    with path.open() as f:
        rows = list(csv.DictReader(f))
    if not rows:
        return
    by: dict[str, list[dict]] = {}
    for r in rows:
        by.setdefault(r["scenario"], []).append(r)
    t = lambda r, k: r[k] == "True"  # noqa: E731
    print(f"\n{'scenario':24} runs  detect  target  category  action  correct  avg s")
    for name, rs in sorted(by.items()):
        n = len(rs)
        secs = [float(r["seconds"] or 0) for r in rs]
        print(f"{name:24} {n:4}  {sum(t(r,'detected') for r in rs):6}  {sum(t(r,'target_ok') for r in rs):6}  "
              f"{sum(t(r,'category_ok') for r in rs):8}  {sum(t(r,'action_ok') for r in rs):6}  "
              f"{sum(t(r,'correct') for r in rs):7}  {sum(secs)/n:5.0f}")
    faults = [r for r in rows if r["fault"] == "True"]
    clean = [r for r in rows if r["fault"] != "True"]
    print(f"\nfault runs: {len(faults)}, fully correct {sum(t(r,'correct') for r in faults)}, "
          f"detected {sum(t(r,'detected') for r in faults)}; no-fault runs: {len(clean)}, "
          f"false alarms {sum(t(r,'false_alarm') for r in clean)}; harness errors "
          f"{sum(1 for r in rows if r['error'].startswith('harness'))}")


if __name__ == "__main__":
    sys.exit(main())
