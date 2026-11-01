# Rehearse

An adaptive AI agent framework for DevOps. Rehearse learns one specific app running on
Kubernetes, diagnoses failures from its logs, metrics and traces, proposes a fix as a typed
action, **rehearses that fix in a sandbox copy of the app** before it touches production, and
fine-tunes a small local model per app so it gets better at that app over time.

Core is Go. Machine learning (data generation, fine-tuning, evaluation) is Python.

## Architecture

Runtime path, when something breaks:

```
Collector -> App Knowledge -> Agent -> Safety Gate -> production
                                ^                         |
                                +----- results saved -----+
```

| Part | Folder | Job |
|------|--------|-----|
| A. Collector | `internal/collector` | Reads the Kubernetes API (client-go), Prometheus (metrics), Loki (logs), Jaeger/OpenTelemetry (traces). Turns raw data into short "facts". |
| B. App Knowledge | `internal/knowledge`, `knowledge/runbooks` | Service map (who calls whom) and normal baselines (what "healthy" looks like). Postgres + pgvector stores runbooks and past incidents for RAG. |
| C. Agent | `internal/agent` | Loop: gather facts, ask the model, call tools, return a JSON answer with one typed action (e.g. `RollbackDeploy`). The agent's credentials are **read-only**. |
| D. Safety Gate | `internal/gate`, `cmd/gate` | Separate service with its own permissions; the only thing allowed to change the cluster. Steps: server dry-run + Kyverno policies -> sandbox rehearsal in a vCluster copy -> human approval -> apply and watch metrics -> auto-undo with a stored inverse if worse than baseline. Everything goes to an audit log. |
| Actions | `internal/actions` | The typed actions the agent may propose, each with an inverse for undo. |
| E. Adaptation Engine | `ml/datagen`, `ml/finetune`, `ml/eval`, `scenarios` | Copies the app into a sandbox, injects known faults (Chaos Mesh / kubectl patch), records the facts the agent would see, labels them with the known answer (JSONL), then QLoRA fine-tunes a small model (Unsloth on Colab), exports GGUF to Ollama, and keeps the adapter only if it beats the base model on the test set. |
| F. Interfaces | `cmd/rehearse`, `dashboard` | CLI (`rehearse why deploy/x`), dashboard, Slack, MCP server. |

Model serving: Ollama on `localhost:11434`, base model + per-app LoRA adapter, called through
its OpenAI-compatible API (`/v1/chat/completions`).

Three ways the agent becomes app-specific, built in this order and measured:
1. Prompting (Phase 3): put the facts in the question.
2. RAG (Phase 5): look up similar runbooks/incidents in pgvector and add them.
3. Fine-tuning (Phase 9): train a LoRA adapter on solved examples from this app.

## Repo layout

```
cmd/rehearse/      CLI entry point
cmd/gate/          Safety Gate service entry point
internal/          Go packages: collector, knowledge, agent, gate, actions
ml/                Python: datagen, finetune, eval
deploy/            kind config, Helm values, manifests
scenarios/         Fault-injection scenarios (break / fix)
knowledge/runbooks Markdown runbooks fed to RAG
dashboard/         Web UI
docs/              Learning notes, one per phase (00-llm-basics.md, ...)
```

## Tech stack

- Go 1.26 (Ubuntu apt), client-go
- Python 3.11+ for ML code (use a `.venv` per ML folder; never install into system Python)
- Kubernetes via kind (local), Helm, kubectl, vCluster (sandbox copies), Kyverno (policies), Chaos Mesh (faults)
- Prometheus, Loki, Grafana, OpenTelemetry / Jaeger
- Postgres + pgvector
- Ollama (local models), `nomic-embed-text` for embeddings
- Unsloth + QLoRA on free Colab GPU for fine-tuning; GGUF export

## Conventions

- Go from Ubuntu apt (`golang-go`, currently 1.26); keep the `go` line in `go.mod` at or below the installed version. `go fmt` and `go vet` clean.
- Python 3.11+, type hints, one virtualenv per project folder.
- Small commits, one idea each, with a clear message.
- Tests for every package (`go test ./...`, `pytest`).
- The agent never gets write access to the cluster. Only the Safety Gate does.
- Never commit model weights, datasets, kubeconfigs or secrets (see `.gitignore`).
- Show install commands before running them; anything needing `sudo` is run by the user.

## I am learning: explain concepts before code

I am new to AI. For every phase:
- Explain the concept first (what it is, why we need it here), then write the code.
- Prefer small, readable code over clever code.
- After writing a file, summarise what each part does and what would break without it.
- Write a short note in `docs/NN-topic.md` for each phase.

## Progress

<!-- One line per finished phase -->
- [x] Phase 0: tools installed, repo + CLAUDE.md, kind cluster (2 nodes Ready), first Ollama call with qwen2.5:3b (2026-11-01)
