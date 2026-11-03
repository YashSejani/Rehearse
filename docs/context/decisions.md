# Decision log

One entry per decision that a future session must not undo by accident. Newest at the bottom.
Format: date · decision · why. Add an entry whenever a phase makes a choice that is not obvious from the code.

## Decided

- **2026-11-01 · Install tools from apt or the official source, never random scripts.** Easy updates with `apt upgrade`. Anything that needs sudo is shown to Yash and run by him.
- **2026-11-01 · Go from Ubuntu apt (1.26), not the go.dev tarball.** Yash prefers apt-managed updates. Consequence: `go.mod` says `go 1.26`; never require a newer toolchain.
- **2026-11-01 · kind installed with `go install`.** No apt package from upstream; `go install` is official and needs no sudo.
- **2026-11-01 · Default local model `qwen2.5:3b`, embeddings `nomic-embed-text`.** Small enough for a CPU-only 14 GiB laptop; Qwen is good at JSON and tool calls.
- **2026-11-01 · kind cluster named `rehearse`, 1 control plane + 1 worker.** Matches the plan; small enough for the laptop.
- **2026-11-01 · No mention of Claude in commits or PRs** (no Co-Authored-By, no footer). Also in `~/.claude/CLAUDE.md`.
- **2026-11-03 · `reference/` is git-ignored.** The AIOpsLab clone (37 MB) is read-only reference material, not our code.
- **2026-11-03 · Reuse AIOpsLab's ideas and fault definitions, not its Python classes.** Our core is Go. AIOpsLab code runs only in Phase 11 evaluation. Keep the MIT copyright line in any file copied from it. Mapping: docs/context/aiopslab-for-rehearse.md.
- **2026-11-04 · HotelReservation = AIOpsLab's variant** (DeathStarBench + MongoDB auth); verified it runs on kind. It already has the Mongo passwords that the revoke-auth / remove-user faults and Phase 11 problems need, so we don't add auth ourselves. Vendored into `deploy/hotel/`, pinned tags, namespace `hotel`.
- **2026-11-04 · App deployments use `strategy: Recreate`.** With 1 replica and RollingUpdate, a bad image or a too-low memory limit leaves the old pod serving, so the fault is invisible (only a stuck rollout). With Recreate the old pod stops first, so the fault is a real outage seen in errors, logs and pod status. Cost: ~1–2 s downtime on normal updates, fine for a lab.
- **2026-11-04 · Logs collected by Grafana Alloy** (Promtail is end-of-life).
- **2026-11-04 · Load generator = k6 Deployment** with Prometheus remote write (Prometheus `enableRemoteWriteReceiver`), so per-endpoint rate, errors and latency are live metrics. wrk2 only prints a summary at the end.
- **2026-11-04 · Phase 1 fault targets:** break-image → `geo`, break-oom → `mongodb-profile` (24Mi limit), break-scale → `user`. Not `profile` for OOM: Go services use only ~6.5 MiB, so a limit low enough to OOM them kills runc itself (`StartError`, not `OOMKilled`). MongoDB (~60 MiB) gives a real `OOMKilled` + CrashLoopBackOff.
- **2026-11-04 · fix-* re-applies the target's objects from `deploy/hotel` (`apply -k -l io.kompose.service=<x>`).** The spec comes from git. fix-oom first removes the memory limit with a JSON patch, because apply cannot delete a field it never set.
- **2026-11-04 · Kubernetes events go into Loki** (`loki.source.kubernetes_events`, `job="kubernetes-events"`). HotelReservation logs nothing when a whole service is down; events are the log evidence for image/OOM/scale faults.
- **2026-11-04 · Host inotify limits raised** (`/etc/sysctl.d/99-rehearse-inotify.conf`: `max_user_instances=512`, `max_user_watches=524288`). With the default 128, kubelet log streaming broke and containerd reported OOM kills as `Error` instead of `OOMKilled`.
- **2026-11-04 · Vendored AIOpsLab app manifests are Apache-2.0** (aiopslab-applications), unlike AIOpsLab itself (MIT). License kept in `deploy/hotel/LICENSE-aiopslab-applications`.
- **2026-11-04 · Grafana admin password `rehearse`** in values (local lab only, not a secret worth protecting).
- **2026-11-04 · The CLI reaches Prometheus and Loki through the Kubernetes API service proxy** (client-go, GET only), so no port-forward is needed. `config.yaml` names each backend as `namespace/service:port`, or as a plain `url` for clusters with an ingress. Later the agent's read-only Role needs `get` on `services/proxy` in `monitoring` only.

## Open (decide in the phase named)

- **Phase 4 · Python 3.11/3.12 for ML and AIOpsLab.** System Python is 3.14. Options: `uv` (manages Python versions per project, no sudo) or the deadsnakes PPA. Recommended: `uv`.
- **Phase 7 · vCluster or temporary namespace for the sandbox.** Measure RAM after Phases 1–6; fall back to a namespace if the laptop is short.
- **Phase 11 · Second app.** Light option (own Go API + Postgres) is safer on 14 GiB RAM than the OpenTelemetry Astronomy Shop.
