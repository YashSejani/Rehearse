#!/usr/bin/env python3
"""Offline comparison of models on the held-out test set (Phase 9).

Sends each test example's input (system + user messages, exactly what the agent sends) to an
OpenAI-compatible endpoint (Ollama), parses the JSON answer and compares it with the label.
No cluster needed, so base vs tuned can be compared on identical inputs.

Usage:
    ml/.venv/bin/python ml/eval/offline.py --models rehearse-base,rehearse-hotel
    ml/.venv/bin/python ml/eval/offline.py --models rehearse-base --limit 20
"""
from __future__ import annotations

import argparse
import json
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CATEGORIES = {"image", "resources", "config", "scaling", "network", "dependency", "unknown"}
ACTIONS = {"RollbackDeploy", "RestartDeployment", "ScaleReplicas", "PatchMemoryLimit", "FixImage", "None"}


def chat(base: str, model: str, messages: list, max_tokens: int = 512) -> tuple[str, dict, float]:
    body = json.dumps({"model": model, "messages": messages, "temperature": 0, "max_tokens": max_tokens,
                       "response_format": {"type": "json_object"}}).encode()
    req = urllib.request.Request(base.rstrip("/") + "/chat/completions", body, {"Content-Type": "application/json"})
    t0 = time.time()
    with urllib.request.urlopen(req, timeout=900) as r:
        out = json.load(r)
    return out["choices"][0]["message"]["content"], out.get("usage", {}), time.time() - t0


def parse(text: str) -> dict | None:
    try:
        a = json.loads(text[text.index("{"): text.rindex("}") + 1])
    except (ValueError, json.JSONDecodeError):
        return None
    ok = (isinstance(a, dict) and a.get("category") in CATEGORIES
          and isinstance(a.get("action"), dict) and a["action"].get("type") in ACTIONS)
    return a if ok else None


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--models", required=True)
    ap.add_argument("--test", default=str(ROOT / "ml/datagen/data/test.jsonl"))
    ap.add_argument("--base-url", default="http://127.0.0.1:11434/v1")
    ap.add_argument("--limit", type=int, default=0)
    args = ap.parse_args()
    rows = [json.loads(l) for l in open(args.test) if l.strip()]
    if args.limit:
        rows = rows[: args.limit]
    out_dir = ROOT / "ml/eval/results"
    out_dir.mkdir(parents=True, exist_ok=True)
    summary = []
    for model in args.models.split(","):
        res = {"model": model, "n": 0, "valid": 0, "category": 0, "action": 0, "target": 0, "correct": 0,
               "false_alarm": 0, "no_fault": 0, "prompt_tokens": 0, "completion_tokens": 0, "seconds": 0.0}
        with open(out_dir / f"offline-{model}.jsonl", "w") as f:
            for i, r in enumerate(rows, 1):
                msgs, label = r["messages"][:-1], json.loads(r["messages"][-1]["content"])
                text, usage, secs = chat(args.base_url, model, msgs)
                a = parse(text)
                res["n"] += 1
                res["seconds"] += secs
                res["prompt_tokens"] += usage.get("prompt_tokens", 0)
                res["completion_tokens"] += usage.get("completion_tokens", 0)
                ok = {"category": False, "action": False, "target": False}
                if a:
                    res["valid"] += 1
                    ok["category"] = a["category"] == label["category"]
                    ok["action"] = a["action"]["type"] == label["action"]["type"]
                    ok["target"] = (a.get("target") or "").replace("deployment/", "") == label["target"] or label["target"] == ""
                    for k in ok:
                        res[k] += ok[k]
                    res["correct"] += all(ok.values())
                if label["action"]["type"] == "None" and label["category"] == "unknown":
                    res["no_fault"] += 1
                    if not a or a["action"]["type"] != "None" or a["category"] != "unknown":
                        res["false_alarm"] += 1
                f.write(json.dumps({"id": r.get("meta", {}).get("id"), "answer": text, "ok": ok, "seconds": round(secs, 1)}) + "\n")
                print(f"{model} {i}/{len(rows)} {r.get('meta', {}).get('fault')}: {'OK' if a and all(ok.values()) else 'x'} ({secs:.0f}s)", flush=True)
        summary.append(res)
    print("\n| model | examples | JSON valid | category | action | target | fully correct | false alarms | avg tokens in/out | avg s |")
    print("|---|---|---|---|---|---|---|---|---|---|")
    for s in summary:
        n = max(s["n"], 1)
        print(f"| {s['model']} | {s['n']} | {s['valid']/n:.0%} | {s['category']/n:.0%} | {s['action']/n:.0%} | "
              f"{s['target']/n:.0%} | {s['correct']/n:.0%} | {s['false_alarm']}/{s['no_fault']} | "
              f"{s['prompt_tokens']//n}/{s['completion_tokens']//n} | {s['seconds']/n:.0f} |")
    json.dump(summary, open(out_dir / "offline-summary.json", "w"), indent=1)


if __name__ == "__main__":
    main()
