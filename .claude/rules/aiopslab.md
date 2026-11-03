---
paths:
  - "scenarios/**"
  - "ml/eval/**"
  - "ml/datagen/**"
  - "reference/**"
---

# Using AIOpsLab (reference/AIOpsLab)

- `reference/AIOpsLab/` is read-only and git-ignored. Never edit it; copy what we need into our own folders.
- Map of what we reuse per phase: docs/context/aiopslab-for-rehearse.md. Read it before porting a fault or scoring logic.
- Where things are (under `reference/AIOpsLab/aiopslab/`):
  - Faults: `generators/fault/inject_virtual.py` (K8s misconfig, scale to 0, missing node, wrong binary), `inject_app.py` (Mongo auth, buggy image), `inject_symp.py` + `chaos-yaml/` (Chaos Mesh), `script/*.sh` (Mongo user scripts).
  - Problems: `orchestrator/problems/registry.py` (IDs like `revoke_auth_mongodb-detection-1`), one folder per problem with `inject_fault`, `recover_fault`, `eval`.
  - Scoring: `orchestrator/tasks/*.py`, `orchestrator/evaluators/quantitative.py`.
  - Agent interface: `../clients/generic_openai.py` (`async get_action(input) -> str`).
- Port the fault *definition* (patch, YAML, script), not the Python class. Add `source:` with the AIOpsLab path to the scenario file, and keep the MIT copyright line on copied text.
- AIOpsLab's HotelReservation uses namespace `test-hotel-reservation`; ours is `hotel`. Adjust selectors and names when porting.
- Running AIOpsLab itself (Phase 11 only): Python 3.11–3.12 + poetry, a separate kind cluster, expect it to install OpenEBS and its own Prometheus.
