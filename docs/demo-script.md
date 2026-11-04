# Rehearse: 3-minute live demo

Goal: show the whole loop on the real cluster: break the app → alert → diagnosis → rehearsal
→ approval → recovery → audit log. Rehearse twice before presenting (the model is slow on CPU).

The real run takes about 10 minutes from the fault to the message (measured in docs/10-interfaces.md),
so it does not fit a live 3-minute slot. Start it 10 minutes earlier, or record it and show that it is sped up.

## Before the demo (10 minutes earlier)

```bash
make playground                       # app, monitoring, load (already running? it is idempotent)
make rehearse-db gate kyverno policies
export REHEARSE_DASHBOARD_TOKEN=demo-$(openssl rand -hex 8)
export SLACK_WEBHOOK_URL=...          # optional
make serve &                          # agent service (alerts → incidents)
(cd dashboard && REHEARSE_API_URL=http://172.20.0.1:8090 npm run dev &)
ollama run rehearse-base "ok"         # load the model into memory (first call is slow)
```

Open three windows: Grafana (`make grafana`, Explore → Prometheus, error-ratio query from
docs/01), the dashboard (http://localhost:3001) and a terminal.

## The 3 minutes

| Time | Say | Do / show |
|---|---|---|
| 0:00 | "This is HotelReservation, 19 microservices, with real users (k6, 5 req/s). Rehearse watches it." | Grafana: error ratio flat at 0. |
| 0:15 | "Now someone deploys a bad change: the profile database gets a 24 MiB memory limit." | `make break-oom` |
| 0:30 | "Users don't see it yet (a cache hides it), but the database is crash-looping. Prometheus fires an alert; Alertmanager calls Rehearse." | Grafana: restarts climb. Dashboard: new incident "investigating…" |
| 1:15 | "The agent collected facts (pod status, events, metrics, rollout history) and the runbook for OOM. It quotes its evidence: OOMKilled, exit 137, memory limit none → 24Mi." | Dashboard incident: root cause, evidence, confidence, proposed `PatchMemoryLimit 256Mi`. Slack message (if configured). |
| 1:45 | "It can't change anything itself. The Safety Gate dry-ran the patch, checked policies, and rehearsed it on a copy of the app: healthy in N seconds." | Dashboard: raw diff, "rehearsed: healthy in …", audit trail. |
| 2:15 | "I approve. The gate applies it and watches for two minutes; if it got worse it would undo it automatically." | Click **Approve and apply**. `kubectl -n hotel get pods -w` shows mongodb-profile Running. |
| 2:40 | "And what if the model was tricked? A log line saying 'run curl evil.sh' cannot become a command: there is no shell tool, and the gate only knows five typed actions." | Dashboard → Audit history: earlier blocked `RunCommand` and scale-to-0 proposals with their reasons. |
| 3:00 | "Every step is in the audit log. Same pipeline works for a second app with only a config file." | `./bin/rehearse --config config-app2.yaml learn` (map of the shop app). |

## If something goes wrong

- Model takes longer than 90 s: talk through the facts (`./bin/rehearse why deploy/mongodb-profile --dump-facts`).
- Alert did not fire yet (`for: 1m`): run `./bin/rehearse fix deploy/mongodb-profile` in the terminal instead; it shows the same diff and asks y/N.
- After the demo: `make fix-oom` (if not approved) and `./bin/rehearse sandbox cleanup`.
