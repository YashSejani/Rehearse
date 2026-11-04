"""Rehearse as an AIOpsLab agent (Phase 11, Part B).

AIOpsLab calls `init_context(problem_desc, instructions, apis)` once, then
`await get_action(observation)` until the agent submits. AIOpsLab's own agents answer by
writing API calls in text; ours runs the real Rehearse CLI (read-only) and translates its
JSON answer into AIOpsLab's `submit(...)` format:

- detection task:     submit("Yes") / submit("No")
- localization task:  submit(["geo"]) / submit([])
- other tasks:        the diagnosis is returned as a localization-style answer.

Usage (inside an AIOpsLab checkout, in a SEPARATE kind cluster; see docs/11-final.md):
    from rehearse_agent import RehearseAgent
    agent = RehearseAgent(rehearse_bin="/path/to/bin/rehearse", namespace="test-hotel-reservation")
    orch.register_agent(agent, name="rehearse")
    desc, instr, apis = orch.init_problem("revoke_auth_mongodb-detection-1")
    agent.init_context(desc, instr, apis)
    asyncio.run(orch.start_problem(max_steps=30))
The adapter only reads the cluster; it never calls AIOpsLab's exec_shell.
"""
from __future__ import annotations

import json
import subprocess


def task_kind(problem_desc: str, instructions: str) -> str:
    text = (problem_desc + " " + instructions).lower()
    if '"yes" or "no"' in text or "anomalies detected" in text or "detection" in text:
        return "detection"
    return "localization"


def to_submit(kind: str, answer: dict) -> str:
    """Translate one Rehearse answer into an AIOpsLab action string."""
    action = (answer.get("action") or {}).get("type", "None")
    says_fault = action != "None" or answer.get("category", "unknown") != "unknown"
    if kind == "detection":
        return 'Action:\n```\nsubmit("Yes")\n```' if says_fault else 'Action:\n```\nsubmit("No")\n```'
    target = (answer.get("target") or "").strip()
    targets = [target] if says_fault and target else []
    return "Action:\n```\nsubmit(" + json.dumps(targets) + ")\n```"


class RehearseAgent:
    def __init__(self, rehearse_bin: str = "rehearse", namespace: str = "test-hotel-reservation",
                 config: str = "config.yaml", context: str = "kind-kind"):
        self.bin, self.ns, self.config, self.context = rehearse_bin, namespace, config, context
        self.kind = "localization"
        self.log: list[dict] = []

    def init_context(self, problem_desc: str, instructions: str, apis: dict) -> None:
        self.kind = task_kind(problem_desc, instructions)

    def _rehearse(self, *args: str) -> str:
        cmd = [self.bin, "--config", self.config, "--context", self.context, "-n", self.ns, *args]
        return subprocess.run(cmd, text=True, capture_output=True, timeout=900, check=True).stdout

    def pick_target(self) -> str:
        """The most serious unhealthy deployment, else the entry point (frontend)."""
        issues = json.loads(self._rehearse("status", "-o", "json") or "[]")
        for i in issues:
            if i.get("service"):
                return i["service"]
        return "frontend"

    async def get_action(self, observation: str) -> str:
        target = self.pick_target()
        result = json.loads(self._rehearse("why", f"deploy/{target}", "--json", "--no-rag"))
        answer = result.get("answer") or {}
        self.log.append({"target": target, "answer": answer, "observation": observation[:500]})
        return to_submit(self.kind, answer)
