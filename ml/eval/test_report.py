"""Run: ml/.venv/bin/python -m unittest ml/eval/test_report.py"""
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from report import compare_table, per_scenario_table, summary  # noqa: E402


def row(scenario, fault=True, correct=False, false_alarm=False, seconds="10"):
    return {"scenario": scenario, "fault": str(fault), "correct": str(correct), "detected": str(correct),
            "target_ok": "True", "category_ok": str(correct), "action_ok": str(correct),
            "false_alarm": str(false_alarm), "seconds": seconds}


class ReportTest(unittest.TestCase):
    def test_no_fault_runs_do_not_count_as_fault_runs(self):
        s = summary([row("a", correct=True), row("a"), row("ok", fault=False, correct=True, false_alarm=True)])
        self.assertEqual((s["runs"], s["correct"], s["no_fault_runs"], s["false_alarms"]), (2, 1, 1, 1))

    def test_partial_run_shows_its_own_totals(self):
        text = per_scenario_table({"x": [row("a", correct=True), row("b")], "y": [row("a")]})
        self.assertIn("| a | 1/1 | 0/1 |", text)
        self.assertIn("| b | 0/1 | 0/0 |", text)

    def test_empty_results_do_not_crash(self):
        self.assertIn("| x | 0 | **0** |", compare_table({"x": []}))


if __name__ == "__main__":
    unittest.main()
