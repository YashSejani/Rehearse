# Phase 8: Generating training data by breaking things (safely)

To fine-tune the model (Phase 9) we need hundreds of solved examples. We make them by
breaking a **sandbox copy** of the app in known ways: because we caused the fault, we know the
right answer.

## 1. The ideas

**A training example = input + correct output.** The input is *exactly* what the agent sees
(the system prompt and the facts message, from `rehearse why --dump-prompt`); the output is the
correct JSON answer, built from what we injected. Chat format, one JSON object per line:

```json
{"messages": [
  {"role": "system", "content": "You are a Kubernetes SRE ..."},
  {"role": "user", "content": "Diagnose deployment mongodb-geo. Facts collected just now:\n<data>\nTARGET deployment: mongodb-geo\nCURRENT STATE: PROBLEM ..."},
  {"role": "assistant", "content": "{\"root_cause\": \"mongodb-geo is OOMKilled because its memory limit of 24Mi ...\", \"category\": \"resources\", ...}"}]}
```

**Train / validation / test.** The model learns from *train*; we watch *validation* during
training to stop before it memorises; we look at *test* only at the end, to report the score.

**Data leakage.** If near-copies of training examples are in the test set, the score lies (the
model only has to remember). Three guards:
1. **Whole fault types are held out for test** (`db-auth-revoked`, `pod-failure`), so the test
   asks: does the model generalise to faults it never saw? These are also the AIOpsLab problem
   types we run in Phase 11, so that test stays fair too.
2. Snapshots of one injection (30 s, 75 s, 120 s) always go to the same split.
3. Near-duplicates (same text after masking numbers, times and pod hashes) are removed.

## 2. What we built

| File | What |
|---|---|
| `ml/datagen/variations.yaml` | per fault type: targets × parameters, how to inject it into the sandbox, and the answer template (root cause, category, target, action, evidence keywords) |
| `ml/datagen/generate.py` | creates one sandbox (`rehearse-sbx-datagen`, its own k6 traffic tagged `target_ns`), then for each variation: inject → record the prompt at 30/75/120 s → undo (`rehearse sandbox sync`) → wait healthy. Resumable (skips recorded variations), shows progress, deletes the sandbox on exit or Ctrl-C |
| `ml/datagen/split.py` | drops records whose evidence is not in the facts, dedupes, caps each fault type, keeps ~10% no-fault, splits by variation |

Evidence lines in the answer are picked from the facts by keyword, so every quote really
appears in the input (the agent's validator would accept it). The sandbox namespace name is
replaced with `hotel` so training text looks like the real app's.

## 3. How to run it

```bash
ml/.venv/bin/python ml/datagen/generate.py --target-examples 500     # hours; resumable
ml/.venv/bin/python ml/datagen/split.py
ls ml/datagen/data/          # raw.jsonl train.jsonl val.jsonl test.jsonl (git-ignored)
```

## 4. Results

490 raw examples from 165 fault variations, on 2026-11-04. After removing 114 near-duplicates:

| Split | Examples | Per category |
|---|---|---|
| train | **318** | config 138, image 87, resources 51, scaling 33, unknown 9 |
| val | 38 | config 6, image 10, resources 17, scaling 4, unknown 1 |
| test | 20 | dependency 4, unknown 16 |

Train by fault type: wrong-image 87, missing-configmap-key 72, failing-readiness 66,
oom-low-limit 51, replicas-zero 33, no-fault 9.

| Check | Result |
|---|---|
| Held-out fault types in train or val | 0 |
| Same variation in two splits | 0 |
| Evidence quote not found in its facts | 0 |
| Sandbox names, poison text or `password=` / `token=` in the data | 0 |
| db-auth-revoked examples containing the real crash line | 6 of 6 |

10 random training examples were read by hand, and every answer is correct. Yash should also read
10 and be able to explain them, because this is one of the phase's done-checks.

**Why 2 fault types are held out:** a model can memorise. If the test used fault types it trained
on, a high score could just mean good memory. `db-auth-revoked` and `pod-failure` never appear in
training, so the test asks whether it can handle a fault it has never seen.

**What went wrong on the way:**

- The first run gave only 242 training examples, because snapshots of one fault are often nearly
  identical. Another 44 variations, mostly scaling and readiness probes, brought it to 318.
- The first 6 db-auth-revoked rows lacked the crash line because of the collector bug in
  docs/05-rag.md, so they were regenerated. Training rows made before that fix were kept: the fix
  only changes the log section when a container's error line was being filtered out.
- The test set is small (20), so its accuracy can only be a rough guide. The live evaluation is
  the main measure.
