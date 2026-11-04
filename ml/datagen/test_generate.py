"""Unit tests for the labelling logic (no cluster): python -m unittest ml/datagen/test_generate.py"""
import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import generate  # noqa: E402
import yaml  # noqa: E402

FACTS = """Diagnose deployment mongodb-geo. Facts collected just now:
<data>
TARGET deployment: mongodb-geo
CURRENT STATE: PROBLEM: 2 unhealthy pods/deployments
## Unhealthy pods and deployments in the namespace
- Pod mongodb-geo-5f9c7b6d8-x2x9q (service mongodb-geo): OOMKilled, restarts 3, exit 137
- [Rollout] revision 2: hotel-reserv-geo-mongo memory limit none -> 16Mi (40s ago)
</data>"""


class TestLabels(unittest.TestCase):
    def setUp(self):
        self.plan = yaml.safe_load((Path(__file__).parent / "variations.yaml").read_text())

    def test_fill(self):
        self.assertEqual(generate.fill(["{target}={param}", {"a": "{ns}"}], target="geo", param="x", ns="n"),
                         ["geo=x", {"a": "n"}])

    def test_evidence_is_exact_quote(self):
        ev = generate.evidence(FACTS, ["OOMKilled", "memory limit"])
        self.assertEqual(len(ev), 2)
        for e in ev:
            self.assertIn(e, FACTS)

    def test_label_matches_agent_schema(self):
        a = generate.label(self.plan["faults"]["oom-low-limit"], "mongodb-geo", "16Mi", FACTS)
        self.assertEqual(a["category"], "resources")
        self.assertEqual(a["action"], {"type": "PatchMemoryLimit", "params": {"deployment": "mongodb-geo", "memory": "256Mi"}})
        self.assertIn("16Mi", a["root_cause"])
        json.dumps(a)
        valid_actions = {"RollbackDeploy", "RestartDeployment", "ScaleReplicas", "PatchMemoryLimit", "FixImage", "None"}
        valid_cats = {"image", "resources", "config", "scaling", "network", "dependency", "unknown"}
        for name, spec in self.plan["faults"].items():
            ans = spec["answer"]
            self.assertIn(ans["action"]["type"], valid_actions, name)
            self.assertIn(ans["category"], valid_cats, name)

    def test_holdout_types_exist(self):
        for h in self.plan["holdout"]:
            self.assertIn(h, self.plan["faults"])


if __name__ == "__main__":
    unittest.main()
