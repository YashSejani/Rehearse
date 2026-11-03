# AIOpsLab for Rehearse: what we reuse and what we change

AIOpsLab (Microsoft, MIT licence) is an *exam centre* for ops agents. Rehearse is an *ops agent
that is safe to use for real*. We copy its exam ideas, but turn them around to protect a real app.
Paths below are relative to `reference/AIOpsLab/aiopslab/`.

## How AIOpsLab runs one problem

`orchestrator/orchestrator.py`:

1. `init_problem(id)` looks up the problem in `orchestrator/problems/registry.py`. A problem is
   **app + fault injector + task level + expected answer**, e.g. `pod_failure_hotel_res-detection-1`.
2. Deploys the app, calls `prob.inject_fault()`, starts the workload (wrk2, about 10 requests/s).
3. Loop up to `max_steps`: `agent.get_action(text)` returns one text call like
   `get_logs("ns", "geo")`. `parser.py` parses it, `perform_action` runs it, and the result goes back.
   Ends on `submit(answer)`.
4. `prob.eval(solution, history, duration)` grades it, then `recover_fault()` always runs
   (also on exceptions and via `atexit`).

The four task levels (`orchestrator/tasks/`) are detection (yes/no), localization (which
service), analysis (system level + fault type) and mitigation (is the live cluster healthy again).

## What we reuse, and in which phase

| AIOpsLab idea | Where in AIOpsLab | Rehearse phase | How we use it |
|---|---|---|---|
| A fault is an **inject + recover pair** with a known answer | `generators/fault/base.py` (`inject_<type>` / `recover_<type>`) | 1, 4 | `make break-*` / `fix-*`, then `scenarios/*.yaml` with `inject`, `cleanup`, `expected` |
| Workload, so faults become visible | `generators/workload/wrk.py` | 1 | Constant load generator on the frontend |
| Concrete HotelReservation faults | `generators/fault/inject_virtual.py` (scale to 0, wrong targetPort, missing node, wrong binary), `inject_app.py` + `script/*.sh` (revoke / remove Mongo user), `inject_symp.py` + `chaos-yaml/` (pod kill/failure, network delay/loss) | 4 | Our 10 scenarios, copied as kubectl patches or Chaos Mesh YAML |
| Small, fixed tool set (Agent-Cloud Interface) | `orchestrator/actions/base.py`: `get_logs`, `get_metrics`, `get_traces`, `exec_shell`, `submit` | 2, 3 | Collector functions become the agent's tools |
| Shrink noisy logs | `orchestrator/actions/log_deduplication.py` | 2 | Collapse repeated lines into "line ×N" |
| Grading by task level, with partial credit | `orchestrator/tasks/*`, `orchestrator/evaluators/quantitative.py` (`is_exact_match`, `is_subset`) | 4 | Score category / target / action separately |
| Cost metrics: time-to-detect etc., steps, tokens | `orchestrator/tasks/*.eval`, `orchestrator/evaluators/quantitative.py` | 4, 9 | Columns in `results.csv` |
| "Nothing is wrong" problems | `noop_detection_hotel_reservation-1` | 4, 8 | No-fault scenarios; false-alarm rate; ~10% of training data |
| Mitigation is checked on the **live cluster**, after a settle window | `orchestrator/tasks/mitigation.py` | 6, 7 | Gate watch step; sandbox pass/fail |
| Record every step | `session.py` | 3 | JSONL trace per investigation |
| Always clean up | `try/except` + `atexit` in `orchestrator.py` | 4, 7 | Harness and sandbox use `defer` / cleanup commands |
| Agent plug-in: `async get_action(input) -> str` | `../clients/generic_openai.py` | 11 | Thin wrapper so our agent sits 3–5 AIOpsLab HotelReservation problems |

Code reuse is limited on purpose. Our core is Go, so we copy **fault definitions** (YAML, scripts,
patches), not Python classes. We run AIOpsLab's own Python code only in Phase 11. MIT allows
copying; keep its copyright line in any file we take.

## What we do differently

| AIOpsLab | Rehearse | Why |
|---|---|---|
| Agent may run `exec_shell` (any kubectl command) | Agent is **read-only**. It proposes one **typed action**; only the Safety Gate writes, with a stored inverse (undo) | A model tricked by a poisoned log line still cannot do harm (Phase 6) |
| Breaks a test app to **grade** an agent | Breaks a **sandbox copy of the real app** to **test one fix** before production (Phase 7) | Rehearsal is the main idea of the project |
| Fault injection only produces a score | Fault injection also produces **labelled training data** (Phase 8) | We know the answer because we caused it, so no human labelling is needed |
| Fixed benchmark; same model for every app | **Per-app adaptation**: service map + baselines, RAG over runbooks, a LoRA adapter per app (Phases 5, 9) | Our claim is "it learns *your* app" |
| Agent replies in text like `api("arg")`, which a regex parses | Native **tool calling** + a final answer validated against a JSON schema (Phase 3) | Fewer parse errors and easier for small models |
| Fresh app deployed per problem; Prometheus + Filebeat/Logstash | App stays running with **learned baselines**; Prometheus + Loki + Grafana | Matches how real teams run, and baselines need history |
| Python orchestrator | Go core, Python only for ML | Kubernetes tooling (client-go) is native Go |

## Gotchas found while reading the code

- **AIOpsLab uses its own HotelReservation.** It deploys from the `aiopslab-applications` submodule
  (not cloned here), with images `yinfangchen/hotelreservation`, MongoDB **with authentication**, and
  namespace `test-hotel-reservation`. Plain DeathStarBench Mongo has no auth, so the revoke-auth and
  remove-user faults only work on AIOpsLab's variant. **Decision for Phase 1:** prefer AIOpsLab's
  HotelReservation manifests if they run on kind, so Phase 4 scenarios and Phase 11 problems use the same app.
- **Their orchestrator is heavy.** Every run installs OpenEBS and its own Prometheus, then deletes them.
  In Phase 11 run it in a separate kind cluster, not next to our monitoring stack.
- **Many problems exist only for other apps.** For example, scale-to-zero is registered only for
  SocialNetwork. For HotelReservation we write those scenarios ourselves.
- **The settle window matters.** CrashLoopBackOff restarts back off exponentially (10s, 20s, 40s...),
  so a health check right after a fix can fail even when the fix is right. Our gate and sandbox
  must wait and re-check, as `tasks/mitigation.py` explains.
