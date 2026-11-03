---
paths:
  - "ml/**"
  - "**/*.py"
  - "**/*.ipynb"
---

# Python / ML rules

- Python 3.11 or 3.12 (system Python is 3.14; see docs/context/decisions.md for how the project Python is installed). One virtualenv per ml/ subfolder or one for `ml/`, never install into system Python.
- Dependencies pinned in `pyproject.toml` or `requirements.txt`.
- Scripts runnable from the repo root (e.g. `python ml/eval/run.py`), with `--help`.
- Training data is chat-format JSONL: `{"messages": [{"role": "system"}, {"role": "user", "content": <facts>}, {"role": "assistant", "content": <answer JSON>}]}`. The assistant answer must match the agent's JSON schema exactly.
- Never leak test data into training: split by whole fault type, and keep AIOpsLab problems used in Phase 11 out of training.
- Data, model files, traces and results go to git-ignored folders (`ml/**/data/`, `ml/**/outputs/`, `traces/`). Commit code and small summary tables only.
- Long runs must be resumable and show progress. Any script that breaks the cluster must clean up in `finally` / signal handlers.
- Explain ML terms (loss, overfitting, LoRA rank, learning rate) in plain words in notebook markdown cells.
