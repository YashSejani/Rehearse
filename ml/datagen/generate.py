#!/usr/bin/env python3
"""Generate labelled training data by breaking a SANDBOX copy of the app (Phase 8).

For each (fault type, target, parameter) variation: inject the fault into the sandbox
namespace, record exactly what the agent would see (`rehearse why --dump-prompt`) at a few
moments after injection, pair it with the known correct answer, then undo the fault.
The real app is never touched.

Usage (repo root):
    ml/.venv/bin/python ml/datagen/generate.py --target-examples 500
    ml/.venv/bin/python ml/datagen/generate.py --only wrong-image --max-variations 2   # quick try
    ml/.venv/bin/python ml/datagen/split.py                                           # then split
Raw records go to ml/datagen/data/raw.jsonl (git-ignored). Re-running skips variations
already recorded, so a long run can be stopped and resumed.
"""
from __future__ import annotations

import argparse
import atexit
import itertools
import json
import random
import re
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
DATA = ROOT / "ml" / "datagen" / "data"
REHEARSE = ROOT / "bin" / "rehearse"
SBX = "rehearse-sbx-datagen"
CTX = "kind-rehearse"


def log(msg: str) -> None:
    print(time.strftime("%H:%M:%S"), msg, flush=True)


def sh(cmd: list[str], check: bool = True, capture: bool = True, timeout: int = 300) -> str:
    p = subprocess.run(cmd, cwd=ROOT, text=True, capture_output=capture, timeout=timeout)
    if check and p.returncode != 0:
        raise RuntimeError(f"{' '.join(cmd[:8])}: {(p.stderr or '').strip()[:300]}")
    return p.stdout or ""


def kubectl(*args: str, check: bool = True) -> str:
    return sh(["kubectl", "--context", CTX, *args], check=check)


def rehearse(*args: str) -> str:
    return sh([str(REHEARSE), "--context", CTX, *args], timeout=180)


def fill(x, **kw):
    """Replace {target}, {param}, {ns} in strings, lists and dicts."""
    if isinstance(x, str):
        for k, v in kw.items():
            x = x.replace("{" + k + "}", str(v))
        return x
    if isinstance(x, list):
        return [fill(i, **kw) for i in x]
    if isinstance(x, dict):
        return {k: fill(v, **kw) for k, v in x.items()}
    return x


# --------------------------------------------------------------------------- sandbox

def sandbox_status() -> list:
    return json.loads(rehearse("-n", SBX, "status", "-o", "json") or "[]")


def sandbox_ready() -> bool:
    """All deployments available and no container waiting (CrashLoopBackOff, pull errors).
    Stricter `rehearse status` also reports a restart in the last 10 minutes, which is right
    for incidents but would make every recovery here take 10 minutes."""
    deps = json.loads(kubectl("-n", SBX, "get", "deployments", "-o", "json"))["items"]
    for d in deps:
        if d["status"].get("availableReplicas", 0) < d["spec"].get("replicas", 1) or d["spec"].get("replicas", 1) == 0:
            return False
    pods = json.loads(kubectl("-n", SBX, "get", "pods", "-o", "json"))["items"]
    for p in pods:
        for cs in p["status"].get("containerStatuses", []):
            if "waiting" in cs.get("state", {}) or not cs.get("ready"):
                return False
    return bool(deps)


def wait_sandbox_healthy(timeout: int = 420) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if sandbox_ready():
                time.sleep(20)  # a little settling, so the next snapshot starts clean
                return True
        except Exception:  # noqa: BLE001
            pass
        time.sleep(10)
    return False


RENDERED: dict[str, dict] = {}   # (kind/name) -> object from deploy/hotel, retargeted to SBX


def render_app() -> list[dict]:
    """The app as committed in git (kustomize), retargeted to the sandbox namespace. Using the
    vendored spec (not the live app) matters: the evaluation may be breaking the live app."""
    docs = [d for d in yaml.safe_load_all(sh(["kubectl", "kustomize", "deploy/hotel"])) if d]
    out = []
    for d in docs:
        if d["kind"] == "Namespace":
            continue
        d["metadata"]["namespace"] = SBX
        if d["kind"] == "Service" and d["spec"].get("type") in ("NodePort", "LoadBalancer"):
            d["spec"]["type"] = "ClusterIP"
            d["spec"].pop("externalTrafficPolicy", None)
            for p in d["spec"].get("ports", []):
                p.pop("nodePort", None)
        RENDERED[f"{d['kind']}/{d['metadata']['name']}"] = d
        out.append(d)
    return out


def apply_docs(docs: list[dict], verb: str = "apply") -> None:
    with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
        yaml.safe_dump_all(docs, f)
    kubectl(verb, "-f", f.name)


def ensure_sandbox(recreate: bool = False) -> None:
    exists = kubectl("get", "namespace", SBX, check=False).strip() != ""
    if exists and not recreate:
        if wait_sandbox_healthy(60):
            return
        log("sandbox unhealthy; recreating")
    if exists:
        delete_sandbox()
        while kubectl("get", "namespace", SBX, check=False).strip():
            time.sleep(5)
    log(f"creating sandbox {SBX} from deploy/hotel")
    kubectl("create", "namespace", SBX)
    kubectl("label", "namespace", SBX, "rehearse.dev/sandbox=true")
    apply_docs(render_app())
    # Its own small load generator, tagged with the sandbox namespace (target_ns).
    manifest = sh(["kubectl", "kustomize", "deploy/loadgen"]).replace("namespace: monitoring", f"namespace: {SBX}")
    with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
        f.write(manifest)
    kubectl("apply", "-f", f.name)
    kubectl("-n", SBX, "set", "env", "deployment/loadgen",
            f"TARGET=http://frontend.{SBX}.svc:5000", f"TARGET_NS={SBX}", "RATE=3")
    # A fresh copy restarts a few times while MongoDB starts, and status reports a recent
    # restart for 10 minutes, so the first wait is long.
    if not wait_sandbox_healthy(timeout=900):
        raise RuntimeError("sandbox did not become healthy")
    time.sleep(60)  # let traffic metrics build up


def reset(target: str) -> None:
    """Put the target's Deployment and Service back to the committed spec (replace, so
    fields a fault added, like env vars, limits or probes, are removed too)."""
    if not RENDERED:
        render_app()
    docs = [RENDERED[k] for k in (f"Deployment/{target}",) if k in RENDERED]
    for d in docs:
        apply_docs([d], verb="replace")


def delete_sandbox() -> None:
    try:
        kubectl("delete", "namespace", SBX, "--wait=false", check=False)
    except Exception as e:  # noqa: BLE001
        log(f"sandbox delete failed: {e} (run: rehearse sandbox cleanup)")


# --------------------------------------------------------------------------- one variation

def inject(spec: dict, target: str, param) -> None:
    kw = dict(target=target, param=param, ns=SBX)
    for step in fill(spec.get("pre", []), **kw):
        kubectl("-n", SBX, *step, check=False)
    for step in fill(spec.get("inject", []), **kw):
        kubectl("-n", SBX, *step)
    if "exec" in spec:
        e = fill(spec["exec"], **kw)
        pod = kubectl("-n", SBX, "get", "pod", "-l", e["selector"], "-o", "jsonpath={.items[0].metadata.name}").strip()
        kubectl("-n", SBX, "exec", pod, "--", "/bin/bash", e["script"])
    if "restart" in spec:
        kubectl("-n", SBX, "delete", "pod", "-l", "io.kompose.service=" + fill(spec["restart"], **kw), "--wait=false")
    if "chaos" in spec:
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(fill(spec["chaos"], **kw))
        kubectl("apply", "-f", f.name)


def undo(spec: dict, target: str, param) -> None:
    kw = dict(target=target, param=param, ns=SBX)
    if "chaos" in spec:
        kubectl("-n", SBX, "delete", "podchaos,networkchaos,stresschaos", "--all", check=False)
    if "undo_exec" in spec:
        e = fill(spec["undo_exec"], **kw)
        pod = kubectl("-n", SBX, "get", "pod", "-l", e["selector"], "-o", "jsonpath={.items[0].metadata.name}", check=False).strip()
        if pod:
            kubectl("-n", SBX, "exec", pod, "--", "/bin/bash", e["script"], check=False)
    reset(target)
    if "restart" in spec:
        kubectl("-n", SBX, "delete", "pod", "-l", "io.kompose.service=" + fill(spec["restart"], **kw), "--wait=false", check=False)
    for step in fill(spec.get("post", []), **kw):
        kubectl("-n", SBX, *step, check=False)


def evidence(user: str, keywords: list[str], k: int = 3) -> list[str]:
    """Pick up to k fact lines that contain the fault's keywords (exact quotes of the facts)."""
    lines = [l.strip().lstrip("- ").strip() for l in user.splitlines()]
    lines = [l for l in lines if l and not l.startswith("#") and not l.startswith("<") and not l.startswith("Diagnose")]
    picked = []
    for kw in keywords:
        for l in lines:
            q = l[:150]  # the agent's prompt asks for quotes under 150 characters
            if kw.lower() in l.lower() and q not in picked:
                picked.append(q)
                break
        if len(picked) >= k:
            break
    return picked


def label(spec: dict, target: str, param, user: str) -> dict:
    a = fill(spec["answer"], target=target, param=param, ns=SBX)
    return {
        "root_cause": a["root_cause"],
        "category": a["category"],
        "target": a.get("target", target),
        "evidence": evidence(user, a.get("evidence_keywords", [])),
        "confidence": 0.9 if a["action"]["type"] != "None" or a["category"] == "unknown" else 0.7,
        "action": a["action"],
    }


def snapshot(ask: str) -> dict:
    out = json.loads(rehearse("-n", SBX, "why", f"deploy/{ask}", "--dump-prompt", "--no-rag"))
    return out


def record_variation(fault: str, spec: dict, target: str, param, snaps: list[int], out) -> int:
    vid = f"{fault}|{target}|{param}"
    ask = target
    n = 0
    t0 = time.time()
    try:
        if fault != "no-fault":
            inject(spec, target, param)
        for s in snaps:
            time.sleep(max(0, t0 + s - time.time()))
            dump = snapshot(ask)
            # Facts can mention the namespace (e.g. "in pod geo-..._<ns>"): make the text look
            # like the real app's, so training and use see the same thing.
            msgs = json.loads(json.dumps(dump["messages"]).replace(SBX, "hotel"))
            user = msgs[-1]["content"]
            if fault != "no-fault" and dump.get("healthy"):
                continue  # the symptom is not visible yet / any more: no honest label
            if fault == "no-fault" and not dump.get("healthy"):
                continue
            ans = label(spec, target, param, user)
            rec = {"id": f"{vid}|t{s}", "variation": vid, "fault": fault, "target": target, "param": str(param),
                   "seconds": s, "messages": msgs + [{"role": "assistant", "content": json.dumps(ans)}]}
            out.write(json.dumps(rec) + "\n")
            out.flush()
            n += 1
    finally:
        if fault != "no-fault":
            undo(spec, target, param)
    return n


def done_variations(path: Path) -> set[str]:
    if not path.exists():
        return set()
    with path.open() as f:
        return {json.loads(l)["variation"] for l in f if l.strip()}


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--target-examples", type=int, default=500)
    ap.add_argument("--only", help="comma-separated fault types")
    ap.add_argument("--max-variations", type=int, default=0, help="per fault type (0 = all)")
    ap.add_argument("--seed", type=int, default=7)
    ap.add_argument("--keep-sandbox", action="store_true")
    args = ap.parse_args()

    plan = yaml.safe_load((ROOT / "ml" / "datagen" / "variations.yaml").read_text())
    DATA.mkdir(parents=True, exist_ok=True)
    raw = DATA / "raw.jsonl"
    done = done_variations(raw)

    work = []
    for fault, spec in plan["faults"].items():
        if args.only and fault not in args.only.split(","):
            continue
        combos = list(itertools.product(spec["targets"], spec["params"]))
        random.Random(args.seed).shuffle(combos)
        if args.max_variations:
            combos = combos[: args.max_variations]
        work += [(fault, spec, t, p) for t, p in combos if f"{fault}|{t}|{p}" not in done]
    # Interleave fault types so a partial run is still balanced.
    random.Random(args.seed).shuffle(work)

    if not args.keep_sandbox:
        atexit.register(delete_sandbox)
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(130))
    ensure_sandbox()

    total = sum(1 for _ in raw.open()) if raw.exists() else 0
    with raw.open("a") as out:
        for i, (fault, spec, target, param) in enumerate(work, 1):
            if total >= args.target_examples:
                break
            log(f"[{i}/{len(work)}] {fault} {target} {param} (examples so far: {total})")
            try:
                total += record_variation(fault, spec, target, param, plan["snapshots"], out)
            except Exception as e:  # noqa: BLE001
                log(f"   !! {e}")
            if not wait_sandbox_healthy(300):
                log("   sandbox did not recover; recreating it")
                ensure_sandbox(recreate=True)
    log(f"done: {total} examples in {raw}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
