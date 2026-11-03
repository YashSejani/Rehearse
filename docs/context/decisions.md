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

## Open (decide in the phase named)

- **Phase 1 · Which HotelReservation build?** Recommended: AIOpsLab's variant (`aiopslab-applications` submodule, images `yinfangchen/hotelreservation`, MongoDB with auth) so revoke-auth / remove-user scenarios and Phase 11 problems work on the same app. Fallback: plain DeathStarBench (`deathstarbench/hotel-reservation:0.3.5`), which has no Mongo auth. Either way: vendor the manifests into `deploy/hotel/`, pin image tags, namespace `hotel`.
- **Phase 1 · How to make break-image / break-oom visible.** With the default RollingUpdate strategy and 1 replica, the old healthy pod keeps serving while the new pod fails, so users see no errors (only a stuck rollout). Choose and document: accept "stuck rollout" as the symptom, or use `strategy: Recreate` on target deployments so the fault causes an outage.
- **Phase 1 · Log collector.** Promtail is end-of-life; use Grafana Alloy unless there is a strong reason not to.
- **Phase 4 · Python 3.11/3.12 for ML and AIOpsLab.** System Python is 3.14. Options: `uv` (manages Python versions per project, no sudo) or the deadsnakes PPA. Recommended: `uv`.
- **Phase 7 · vCluster or temporary namespace for the sandbox.** Measure RAM after Phases 1–6; fall back to a namespace if the laptop is short.
- **Phase 11 · Second app.** Light option (own Go API + Postgres) is safer on 14 GiB RAM than the OpenTelemetry Astronomy Shop.
