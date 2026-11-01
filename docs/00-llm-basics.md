# 00 · LLM basics

Every later phase of Rehearse sends text to a language model and reads its answer.
This note explains what that model actually is, and how we talk to it.

## What is an LLM?

A **large language model** is a program that does one thing: given some text, it predicts
the most likely **next piece of text**. Then it appends that piece and predicts again, over and
over, until it decides to stop. A whole "answer" is just that loop running many times.

It learned to predict well by reading a huge amount of text during **training**. Training
adjusted billions of numbers inside the model, called **weights** (or parameters). The weights
are the model; the program around them is small.

Things that follow from this:

- The model has no live access to your cluster. It only knows (a) what it saw in training and
  (b) what you put in the message. That is why the agent must *collect facts* first.
- It can sound confident and still be wrong ("hallucination"). That is why every action goes
  through the Safety Gate.
- An **instruct** (or chat) model is a base model given extra training to follow instructions
  and answer in a conversation. We always use instruct models.

## Tokens

Models don't read letters or whole words. They read **tokens**: common chunks of text.
Roughly, 1 token ≈ ¾ of an English word, so 100 tokens ≈ 75 words.

```
"kubectl rollout undo" -> ["k", "ubectl", " roll", "out", " undo"]   (example split)
```

Tokens matter because everything is measured in them:

- **Context window**: the maximum tokens the model can see at once (your prompt + its answer).
  Pasting 10,000 log lines won't fit; the Collector must summarise.
- **Speed**: a laptop CPU generates maybe 5–20 tokens per second for a small model.
- **Cost**: cloud APIs charge per token. Local models are free but slower.

## Model sizes

"3B" or "7B" means 3 or 7 **billion** weights.

| Size | Rough RAM needed (4-bit) | Notes |
|------|--------------------------|-------|
| 1–3B | 2–3 GB | Runs on any laptop. Fine for structured, narrow tasks. |
| 7–8B | 5–6 GB | Noticeably smarter, slower on CPU. |
| 70B  | 40+ GB | Needs a big GPU server. |

**Quantization** stores each weight in fewer bits (e.g. 4 instead of 16), which cuts memory
about 4x for a small loss in quality. Ollama models are quantized by default (GGUF format).

Our bet in Rehearse: a **small** model that has been fine-tuned on *one* app can beat a bigger
general model on that app. Phase 9 tests that bet.

## Ollama

Ollama downloads models and runs them locally as a server on `localhost:11434`. It speaks
the same HTTP API shape as OpenAI, so our Go code can use any OpenAI-compatible client and we
can swap models without changing code.

Two models for this project:

```bash
ollama pull qwen2.5:3b          # small instruct (chat) model, good at JSON and tool calls
ollama pull nomic-embed-text    # embedding model: turns text into a vector (used for RAG, Phase 5)
```

Chat in the terminal:

```bash
ollama run qwen2.5:3b
```

## Calling the model over HTTP

```bash
curl -s http://localhost:11434/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen2.5:3b",
    "messages": [
      {"role": "system", "content": "You are a Kubernetes SRE. Answer in one sentence."},
      {"role": "user",   "content": "A pod is in CrashLoopBackOff. What does that mean?"}
    ],
    "temperature": 0
  }'
```

What we send:

- `model`: which model Ollama should use.
- `messages`: the conversation so far. Each has a `role`:
  - `system`: standing instructions (who the model is, how to answer).
  - `user`: the question.
  - `assistant`: the model's earlier replies (we add these for multi-turn chats).
  The model has no memory between calls; you resend the whole history each time.
- `temperature`: randomness. `0` = always the most likely token (repeatable, what we want for
  diagnosis). Higher = more varied.

What comes back (shortened):

```json
{
  "id": "chatcmpl-123",
  "object": "chat.completion",
  "created": 1793548800,
  "model": "qwen2.5:3b",
  "choices": [
    {
      "index": 0,
      "message": { "role": "assistant", "content": "CrashLoopBackOff means the container keeps crashing on start and Kubernetes is waiting longer and longer before restarting it." },
      "finish_reason": "stop"
    }
  ],
  "usage": { "prompt_tokens": 38, "completion_tokens": 27, "total_tokens": 65 }
}
```

- `id`, `object`, `created`: bookkeeping (unique id, response type, Unix timestamp).
- `choices[0].message.content`: the answer text. This is the field our code reads.
- `finish_reason`: why it stopped. `stop` = finished naturally; `length` = hit the token
  limit (answer is cut off, a bug to watch for).
- `usage`: tokens in (`prompt_tokens`), tokens out (`completion_tokens`) and the total.
  This is how we measure prompt size and speed later.

Tip: pipe through `jq` to pull out only the answer:
`curl ... | jq -r '.choices[0].message.content'`

## Check yourself

- Why can't the model tell you which pod is failing unless you send it the pod list?
- What happens to `finish_reason` if you add `"max_tokens": 5` to the request? Try it.
- Run the same request with `"temperature": 1` three times. What changes?
