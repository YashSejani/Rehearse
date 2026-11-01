# 00 · Installing the tools

Machine: Ubuntu 26.04, x86_64. Already present: Docker 29, git, make, curl, jq, Python 3.14.

Each tool is installed from its **official** source, chosen so updates are easy:

| Tool | Source | How it updates |
|------|--------|----------------|
| kubectl | Kubernetes apt repo (pkgs.k8s.io) | `sudo apt upgrade` (patch releases); change `v1.37` in the repo line for a new minor |
| Helm | Helm's official apt repo | `sudo apt upgrade` |
| Go | Ubuntu apt (`golang-go`, 1.26) | `sudo apt upgrade` |
| kind | `go install` from the official module | Re-run `go install ...@<new version>` |
| Ollama | Official install script (adds a systemd service) | Re-run the script |

## kubectl (talks to any Kubernetes cluster)

```bash
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.37/deb/Release.key | sudo gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.37/deb/ /' | sudo tee /etc/apt/sources.list.d/kubernetes.list
sudo apt-get update && sudo apt-get install -y kubectl
```

## Helm (package manager for Kubernetes; used from Phase 1)

```bash
curl -fsSL https://packages.buildkite.com/helm-linux/helm-debian/gpgkey | gpg --dearmor | sudo tee /usr/share/keyrings/helm.gpg > /dev/null
echo 'deb [signed-by=/usr/share/keyrings/helm.gpg] https://packages.buildkite.com/helm-linux/helm-debian/any/ any main' | sudo tee /etc/apt/sources.list.d/helm-stable-debian.list
sudo apt-get update && sudo apt-get install -y helm
```

## Go (Ubuntu apt, 1.26)

```bash
sudo apt-get install -y golang-go
echo 'export PATH=$PATH:$HOME/go/bin' >> ~/.bashrc && source ~/.bashrc
go version
```

`$HOME/go/bin` is where `go install` puts programs such as kind.

## kind (no sudo needed)

```bash
go install sigs.k8s.io/kind@v0.33.0 && kind version
```

## Ollama

```bash
curl -fsSL https://ollama.com/install.sh | sh
ollama pull qwen2.5:3b
ollama pull nomic-embed-text
```

The script asks for sudo itself: it installs to `/usr/local`, creates an `ollama` user and a
systemd service so the server is always running on `localhost:11434`.
To read it before running: `curl -fsSL https://ollama.com/install.sh | less`.

## Python

Python 3.14 is already installed with `venv`. ML code will use a virtualenv per folder; if a
library (e.g. Unsloth) needs an older Python, we'll add one then. Fine-tuning itself runs on Colab.

## Phase 0 "done when" checks

```bash
make cluster              # kubectl get nodes shows 2 nodes Ready
ollama run qwen2.5:3b "hello"
```

Then run the curl example in `00-llm-basics.md`.
