#!/usr/bin/env python3
"""Clean and split the generated data (Phase 8).

- drop records whose answer quotes evidence that is not in the facts
- deduplicate near-identical records (numbers, times and pod hashes masked)
- balance categories (cap each fault type), keep about 10% no-fault examples
- split: whole held-out fault types -> test.jsonl; the rest -> train.jsonl / val.jsonl (90/10,
  by variation so snapshots of one injection never land in both)

Usage: ml/.venv/bin/python ml/datagen/split.py [--cap 120]
"""
from __future__ import annotations

import argparse
import collections
import hashlib
import json
import random
import re
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
DATA = ROOT / "ml" / "datagen" / "data"


def norm(text: str) -> str:
    text = re.sub(r"\b[a-z0-9]+-[a-z0-9]{8,10}-[a-z0-9]{5}\b", "<pod>", text)
    text = re.sub(r"\d+(\.\d+)?", "N", text)
    return re.sub(r"\s+", " ", text)


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--cap", type=int, default=120, help="max examples per fault type")
    ap.add_argument("--seed", type=int, default=7)
    args = ap.parse_args()
    plan = yaml.safe_load((ROOT / "ml" / "datagen" / "variations.yaml").read_text())
    holdout = set(plan["holdout"])
    recs = [json.loads(l) for l in (DATA / "raw.jsonl").open() if l.strip()]

    seen, kept, dropped = set(), [], collections.Counter()
    for r in recs:
        user = r["messages"][-2]["content"]
        ans = json.loads(r["messages"][-1]["content"])
        # same rules as the generator now applies: unique quotes, at most 150 characters
        ev = []
        for e in ans["evidence"]:
            if e[:150] not in ev:
                ev.append(e[:150])
        ans["evidence"] = ev[:3]
        r["messages"][-1]["content"] = json.dumps(ans)
        if any(e not in user for e in ans["evidence"]):
            dropped["evidence not in facts"] += 1
            continue
        if r["fault"] != "no-fault" and not ans["evidence"]:
            dropped["no evidence found"] += 1
            continue
        h = hashlib.sha256((r["fault"] + norm(user)).encode()).hexdigest()
        if h in seen:
            dropped["near-duplicate"] += 1
            continue
        seen.add(h)
        kept.append(r)

    rng = random.Random(args.seed)
    by_fault = collections.defaultdict(list)
    for r in kept:
        by_fault[r["fault"]].append(r)
    balanced = []
    for fault, rs in by_fault.items():
        rng.shuffle(rs)
        balanced += rs[: args.cap]
    faults = [r for r in balanced if r["fault"] != "no-fault"]
    clean = [r for r in balanced if r["fault"] == "no-fault"][: max(1, len(faults) // 9)]  # ~10%
    balanced = faults + clean

    test = [r for r in balanced if r["fault"] in holdout]
    rest = [r for r in balanced if r["fault"] not in holdout]
    variations = sorted({r["variation"] for r in rest})
    rng.shuffle(variations)
    val_vars = set(variations[: max(1, len(variations) // 10)])
    val = [r for r in rest if r["variation"] in val_vars]
    train = [r for r in rest if r["variation"] not in val_vars]
    # a few no-fault examples in test too, so false alarms are measured there
    nf = [r for r in train if r["fault"] == "no-fault"][:5]
    test += nf
    train = [r for r in train if r not in nf]

    for name, rs in (("train", train), ("val", val), ("test", test)):
        rng.shuffle(rs)
        with (DATA / f"{name}.jsonl").open("w") as f:
            for r in rs:
                f.write(json.dumps({"messages": r["messages"], "meta": {k: r[k] for k in ("id", "fault", "target", "param", "seconds")}}) + "\n")
    print(f"raw {len(recs)}, kept {len(kept)}, dropped {dict(dropped)}")
    print(f"{'split':6} {'total':>5}  per category")
    for name, rs in (("train", train), ("val", val), ("test", test)):
        cats = collections.Counter(json.loads(r["messages"][-1]["content"])["category"] for r in rs)
        print(f"{name:6} {len(rs):5}  {dict(sorted(cats.items()))}")
    print(f"held out for test: {sorted(holdout)}")


if __name__ == "__main__":
    main()
