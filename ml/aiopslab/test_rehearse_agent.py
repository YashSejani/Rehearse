"""Unit tests for the AIOpsLab adapter (no cluster): python -m unittest ml/aiopslab/test_rehearse_agent.py"""
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from rehearse_agent import task_kind, to_submit  # noqa: E402


class TestTranslate(unittest.TestCase):
    def test_detection(self):
        fault = {"category": "dependency", "target": "mongodb-geo", "action": {"type": "None"}}
        healthy = {"category": "unknown", "target": "", "action": {"type": "None"}}
        self.assertIn('submit("Yes")', to_submit("detection", fault))
        self.assertIn('submit("No")', to_submit("detection", healthy))

    def test_localization(self):
        a = {"category": "image", "target": "geo", "action": {"type": "RollbackDeploy"}}
        self.assertIn('submit(["geo"])', to_submit("localization", a))
        self.assertIn("submit([])", to_submit("localization", {"category": "unknown", "action": {"type": "None"}}))

    def test_task_kind(self):
        self.assertEqual(task_kind("", 'submit "Yes" or "No" to indicate whether there are anomalies detected'), "detection")
        self.assertEqual(task_kind("", "submit a list of faulty components"), "localization")


if __name__ == "__main__":
    unittest.main()
