# Environment: the machine and tools Rehearse runs on

Verified on 2026-11-01. Update this file when a tool is upgraded or added.

## Machine

| | |
|---|---|
| OS | Ubuntu 26.04 LTS, x86_64 |
| CPU / RAM | 8 cores, ~14 GiB RAM (plan for "16 GB laptop" sizing, but leave headroom) |
| GPU | None. Ollama runs on CPU. Fine-tuning happens on Google Colab (free T4). |
| Shell | bash. `~/.bashrc` line adds `$HOME/go/bin` to PATH. Terminals opened before that change need `source ~/.bashrc`. |

## Installed tools and where they come from

| Tool | Version | Source (how to update) |
|---|---|---|
| Docker Engine | 29.9.2 | Docker apt repo (`apt upgrade`). User is in the `docker` group, so no sudo needed. |
| kubectl | 1.37.2 | pkgs.k8s.io apt repo, pinned to the v1.37 line (`apt upgrade`; change `v1.37` in `/etc/apt/sources.list.d/kubernetes.list` for a new minor) |
| Helm | **4.3.0** | Helm's apt repo on Buildkite (`apt upgrade`). Helm 4, not 3: most chart docs still say Helm 3 and work the same; check flags if a command fails. |
| Go | 1.26.8 | Ubuntu apt `golang-go` (`apt upgrade`). Keep `go.mod` at `go 1.26`. |
| kind | 0.33.0 | `go install sigs.k8s.io/kind@v0.33.0`, binary in `~/go/bin` |
| Ollama | 0.40.13 | Official script, systemd service, API on `http://127.0.0.1:11434` (re-run the script to update) |
| Python | 3.14.4 (system) | Ubuntu. ML code needs 3.11 or 3.12 (AIOpsLab requires `>=3.11,<3.13`). Not installed yet; decide how in Phase 4 (see decisions.md). |
| git, make, curl, jq | system | Ubuntu |

Not installed yet (added in later phases): Chaos Mesh (Phase 4), Postgres + pgvector (Phase 5), Kyverno (Phase 6), vCluster CLI (Phase 7), Node.js for the dashboard (Phase 10).

## Ollama models

| Model | Size | Use |
|---|---|---|
| `qwen2.5:3b` | 1.9 GB | Default chat / agent model (good at JSON and tool calls) |
| `nomic-embed-text` | 274 MB | Embeddings for RAG (Phase 5) |

Speed on this CPU: a 32-token answer took about 12 s including model load. Keep prompts short.

## Kubernetes cluster

- kind cluster `rehearse`, kubectl context `kind-rehearse`, Kubernetes v1.37.0.
- Nodes: `rehearse-control-plane`, `rehearse-worker`. Config: `deploy/kind-config.yaml`.
- `make cluster` creates it (idempotent, waits for Ready); `make delete-cluster` removes it to free RAM.
- The cluster survives reboots only if Docker restarts the node containers; if `kubectl get nodes` fails after a reboot, run `docker start rehearse-control-plane rehearse-worker` or recreate with `make cluster`.

## GitHub

- Repo: https://github.com/YashSejani/Rehearse (branch `main`, HTTPS remote `origin`).
- Commits are authored by YashSejani only. Never add Claude co-author lines or mentions.
