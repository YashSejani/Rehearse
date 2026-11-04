#!/usr/bin/env python3
"""Turn evaluation CSVs into the Markdown tables used in the docs.

    ml/.venv/bin/python ml/eval/report.py baseline v2-no-rag v2-rag
    ml/.venv/bin/python ml/eval/report.py v2-rag --detail

Each name is a file ml/eval/results/<name>.csv written by ml/eval/run.py. A run that is still
going is reported as it stands: the "Runs" columns show how complete it is.
"""
import argparse
import csv
import sys
from pathlib import Path

RESULTS = Path(__file__).resolve().parent / "results"


def load(label: str, folder: Path = RESULTS) -> list[dict]:
    with (folder / f"{label}.csv").open(newline="") as f:
        return list(csv.DictReader(f))


def yes(row: dict, field: str) -> bool:
    return row[field] == "True"


def summary(rows: list[dict]) -> dict:
    faults = [r for r in rows if yes(r, "fault")]
    healthy = [r for r in rows if not yes(r, "fault")]
    count = lambda field: sum(yes(r, field) for r in faults)
    timed = [float(r["seconds"]) for r in faults if r["seconds"]]
    return {
        "runs": len(faults),
        "correct": count("correct"),
        "detected": count("detected"),
        "target": count("target_ok"),
        "category": count("category_ok"),
        "action": count("action_ok"),
        "no_fault_runs": len(healthy),
        "false_alarms": sum(yes(r, "false_alarm") for r in healthy),
        "avg_seconds": sum(timed) / len(timed) if timed else 0.0,
    }


def table(header: list[str], rows: list[list]) -> str:
    lines = ["| " + " | ".join(header) + " |", "|" + "---|" * len(header)]
    lines += ["| " + " | ".join(str(c) for c in row) + " |" for row in rows]
    return "\n".join(lines)


def compare_table(runs: dict[str, list[dict]]) -> str:
    rows = []
    for label, data in runs.items():
        s = summary(data)
        rows.append([label, s["runs"], f"**{s['correct']}**", s["detected"], s["target"], s["category"], s["action"],
                     f"{s['false_alarms']}/{s['no_fault_runs']}", round(s["avg_seconds"])])
    return table(["Setting", "Fault runs", "Fully correct", "Detected", "Target", "Category", "Action",
                  "False alarms", "Avg s"], rows)


def per_scenario_table(runs: dict[str, list[dict]]) -> str:
    """Fully correct runs per scenario, one column per setting."""
    scenarios = []
    for data in runs.values():
        for r in data:
            if r["scenario"] not in scenarios:
                scenarios.append(r["scenario"])
    rows = []
    for name in scenarios:
        row = [name]
        for data in runs.values():
            these = [r for r in data if r["scenario"] == name]
            row.append(f"{sum(yes(r, 'correct') for r in these)}/{len(these)}")
        rows.append(row)
    return table(["Scenario", *runs], rows)


def detail_table(data: list[dict]) -> str:
    groups: dict[str, list[dict]] = {}
    for r in data:
        groups.setdefault(r["scenario"], []).append(r)
    rows = []
    for name, these in groups.items():
        s = lambda field: sum(yes(r, field) for r in these)
        avg = sum(float(r["seconds"] or 0) for r in these) / len(these)
        rows.append([name, len(these), s("detected"), s("target_ok"), s("category_ok"), s("action_ok"),
                     f"**{s('correct')}**", round(avg)])
    return table(["Scenario", "Runs", "Detected", "Target", "Category", "Action", "Fully correct", "Avg s"], rows)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("labels", nargs="+", help="result names, e.g. baseline v2-no-rag v2-rag")
    ap.add_argument("--detail", action="store_true", help="also print a per-scenario table for each label")
    args = ap.parse_args()

    runs = {label: load(label) for label in args.labels}
    print(compare_table(runs))
    if len(runs) > 1:
        print("\n" + per_scenario_table(runs))
    if args.detail:
        for label, data in runs.items():
            print(f"\n### {label}\n\n{detail_table(data)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
