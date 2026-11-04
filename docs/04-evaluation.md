# Phase 4: Evaluation harness and the baseline

A script breaks the app in 10 known ways (plus 2 "nothing is broken" checks), asks the agent,
scores the answer, and always cleans up. The score of today's agent is the **baseline** that
RAG (Phase 5) and fine-tuning (Phase 9) must beat.

## 1. The ideas

- **Evaluation:** never trust "it seems better". Run the same test set and compare numbers.
- **Baseline:** the score of the simplest version. Without it you cannot prove an improvement.
- **Variance:** a model can answer differently each run (and the cluster is never exactly the
  same twice), so each scenario runs **3 times**.

## 2. What we built

| Part | Where |
|---|---|
| Python 3.12 via `uv` 0.13.5 (official release binary, checksum verified, `~/.local/bin`) | `ml/pyproject.toml`, `ml/uv.lock`, `ml/.venv` |
| Chaos Mesh 2.8.6 (containerd runtime, 1 controller, no dashboard) | `deploy/chaos-mesh-values.yaml`, `make chaos-mesh` |
| 12 scenarios: how to inject, when the symptom is visible, the right answer, cleanup | `scenarios/*.yaml` (+ `scenarios/chaos/` ported from AIOpsLab with its MIT line) |
| Harness: inject → wait for symptom → `rehearse why --json` → score → **cleanup in `finally` + `atexit` + SIGTERM** → wait healthy → prove every Deployment/Service spec is identical to before | `ml/eval/run.py`, `make eval` |

Scoring like AIOpsLab's tasks: **detection** (says something is wrong; for no-fault: says
nothing is wrong), **localization** (target in the expected list, like `is_subset`), **category**,
**action**, plus model calls, tool calls, tokens and seconds. Results append to
`ml/eval/results/<label>.csv` (git-ignored); a run can be stopped and resumed. The harness copies
`bin/rehearse` to a frozen binary at start, so rebuilding during a 3-hour run changes nothing.

| Scenario | Injected by | AIOpsLab source |
|---|---|---|
| wrong-image | `make break-image` (geo) | `inject_app.py` misconfig_app |
| oom-low-limit | `make break-oom` (mongodb-profile 24Mi) | ours |
| replicas-zero | `make break-scale` (user) | `inject_virtual.py` scale_pods_to_zero |
| db-auth-revoked | `revoke-admin-geo-mongo.sh` in mongodb-geo, restart geo | `inject_app.py` revoke_auth + script |
| wrong-targetport | frontend Service targetPort 5999 | `inject_virtual.py` misconfig_k8s |
| missing-configmap-key | rate env from a key that does not exist | ours |
| failing-readiness | frontend readiness probe on a 404 path | ours |
| pod-failure | Chaos Mesh PodChaos on recommendation | `inject_symp.py` + `pod-failure.yaml` |
| network-delay | Chaos Mesh NetworkChaos 2 s on search | `inject_symp.py` + `network-delay.yaml` |
| cpu-stress | Chaos Mesh StressChaos on geo | ours |
| no-fault-frontend / -reservation | nothing | `noop_detection_hotel_reservation-1` |

Before the baseline, a `--no-agent` run checked that all 12 inject, show their symptom and
clean up to the identical spec. It found two **measurement** bugs that also hid faults from
the agent:

| Found | Why | Fix |
|---|---|---|
| network-delay never visible | k6's ready-made `k6_http_req_duration_p95` is cumulative since k6 started (hours), so minutes of 2 s requests barely move it | k6 sends latency as a **native histogram** (`K6_FEATURES=native-histograms`); p95 = `histogram_quantile` over the window |
| wrong-targetport never visible | k6 reused keep-alive connections, which kept the old route | `noConnectionReuse: true` (new connection per request, like many real users) |

## 3. The baseline (base model `rehearse-base`, no RAG, 3 runs per scenario, 2026-11-04)

| Scenario | Runs | Detected | Target | Category | Action | **Fully correct** | Avg s |
|---|---|---|---|---|---|---|---|
| wrong-image | 3 | 2 | 2 | 2 | 2 | **2** | 133 |
| oom-low-limit | 3 | 3 | 3 | 2 | 2 | **2** | 100 |
| replicas-zero | 3 | 3 | 3 | 3 | 2 | **2** | 69 |
| missing-configmap-key | 3 | 3 | 3 | 2 | 1 | **1** | 104 |
| db-auth-revoked | 3 | 3 | 3 | 0 | 0 | 0 | 165 |
| failing-readiness | 3 | 3 | 3 | 0 | 0 | 0 | 61 |
| wrong-targetport | 3 | 2 | 3 | 1 | 1 | 0 | 152 |
| network-delay | 3 | 3 | 3 | 0 | 0 | 0 | 48 |
| pod-failure | 3 | 0 | 3 | 3 | 3 | 0 | 179 |
| cpu-stress | 3 | 1 | 3 | 0 | 2 | 0 | 43 |
| no-fault-frontend | 3 | 3 | 3 | 3 | 3 | **3** | 1 |
| no-fault-reservation | 3 | 3 | 3 | 3 | 3 | **3** | 1 |

**Summary: base model 7/30 fault runs fully correct, 23/30 detected, 29/30 right target,
0/6 false alarms (with triage).**

Without triage (`--always-ask`, the model judges the healthy app itself): **5/6 false alarms**.
So the deterministic health check from Phase 3 is not optional for a 3B model.

What the numbers say: the small model almost always finds *where* the problem is, but often
gets *what kind* and *what to do* wrong (e.g. `RestartDeployment` for an OOM, `PatchMemoryLimit`
for a network delay, `ScaleReplicas` for a failing readiness probe). Faults whose fix is outside
the catalogue (DB permissions, Service port, network) are never fully right: the model always
wants to *do* something.

## 4. The worst failure, from its trace

`db-auth-revoked`, run 1 (`traces/20261104-100013-geo.jsonl`). The agent answered "geo is stuck
due to a bad image", category image, `RollbackDeploy`. Reading the trace:

1. The facts had **no log line** from geo ("no error lines"): geo had just crashed (exit 1)
   and its last log lines ("not authorized on geo-db") had not reached Loki yet.
2. The only *change* in the facts was the previous scenario's image rollout on geo, 11 minutes
   earlier. With no better evidence, the model took that history as the cause.
3. Its action even passed the old *broken* image as a parameter (`image: ...does-not-exist`);
   the validator drops unknown params, and the gate (Phase 6) would block or undo a bad change.

Fix for the next phases: the collector now also reads the **previous container's log straight
from the Kubernetes API** (`kubectl logs --previous`) for crashed pods, so the crash reason is
in the facts even before Loki has it. RAG is then measured *with and without* this collector, so
the RAG effect stays separate.

## 5. How to run it

```bash
make chaos-mesh
ml/.venv/bin/python ml/eval/run.py --no-agent --runs 1 --label check     # scenarios inject and clean up?
make eval LABEL=baseline ARGS=--no-rag                                    # ~3 hours, resumable
ml/.venv/bin/python ml/eval/run.py --label x --only wrong-image --runs 1  # one scenario
```

Known harness behaviour: after `pod-failure`, recommendation sometimes needs longer than the
7-minute health wait (CrashLoopBackOff back-off); the run records "NOT RESTORED: healthy=False
changed=[]" (specs identical) and the next scenario starts once the app is healthy.
