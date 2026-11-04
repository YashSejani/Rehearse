"""Builds train.ipynb (kept as a script so the notebook stays reviewable in git diffs)."""
import json
from pathlib import Path

cells = []
md = lambda s: cells.append({"cell_type": "markdown", "metadata": {}, "source": s.strip("\n")})
code = lambda s: cells.append({"cell_type": "code", "metadata": {}, "execution_count": None, "outputs": [], "source": s.strip("\n")})

md("""
# Rehearse Phase 9: fine-tune the agent model with QLoRA (Kaggle or Google Colab, free T4 GPU)

**Goal:** teach `Qwen2.5-3B-Instruct` (the same model the agent uses, `qwen2.5:3b` in Ollama) to
answer HotelReservation incidents in our exact JSON format, then bring it back to the laptop as
a GGUF file for Ollama and measure whether it beats the base model.

**On Kaggle (preferred, longer sessions):** upload `train.jsonl` and `val.jsonl` from
`ml/datagen/data/` as a **private** Dataset and attach it with Add Input. In Settings choose
Accelerator **GPU T4 x2** and turn **Internet on** (needs a verified phone number). Then use
Save Version → **Save & Run All**: it keeps running after you close the browser, and the files
appear in the version's Output tab.

**On Colab:** Runtime → Change runtime type → **T4 GPU**. Run all, and upload the two files when
asked. Keep the tab open.

The data files are git-ignored and never committed or made public: they contain our app's logs.

### The ideas in plain words
- **Fine-tuning** nudges the model's weights using our examples (facts → correct answer).
- **LoRA** freezes the 3 billion original weights and trains small extra matrices beside some
  layers (about 1% of the size). Cheap, and the original model is untouched.
- **QLoRA** also stores the frozen model in 4-bit, so it fits in the T4's 15 GB.
- **Loss** = how wrong the model is on the examples (lower is better). We plot training loss and
  validation loss (examples it never trains on).
- **Overfitting** = training loss keeps falling but validation loss rises: the model memorises
  our examples instead of learning the pattern.
- **GGUF / quantisation** = a compressed file format Ollama runs on a laptop CPU.
""")
code("""
%%capture
# Unsloth's API changes; check https://docs.unsloth.ai if a cell fails.
!pip install unsloth
""")
code("""
import glob, os, shutil

ON_KAGGLE = os.path.exists("/kaggle/input")
os.environ["CUDA_VISIBLE_DEVICES"] = "0"   # Kaggle offers 2 GPUs; this notebook trains on one

if ON_KAGGLE:
    for name in ("train.jsonl", "val.jsonl"):
        found = glob.glob(f"/kaggle/input/**/{name}", recursive=True)
        assert found, f"{name} not found: attach the private dataset with Add Input"
        shutil.copy(found[0], name)
else:
    from google.colab import files
    files.upload()   # choose train.jsonl and val.jsonl
print(open("train.jsonl").read().count("\\n"), "training examples")
""")
md("""
### Hyperparameters (what each knob means)
| Name | Value | In plain words |
|---|---|---|
| `max_seq_length` | 4096 | Longest example (facts + answer) in tokens. Our prompts are ~1,500–2,500 tokens. |
| LoRA rank `r` | 16 | Size of the small extra matrices. Bigger = can learn more, but overfits faster. 8–32 is normal. |
| `lora_alpha` | 16 | Scale of the LoRA update. Usually equal to `r`. |
| `lora_dropout` | 0 | Randomly switching off parts during training to fight overfitting; 0 is fastest and Unsloth's default. |
| learning rate | 2e-4 | Step size of each update. Too high = unstable loss, too low = learns nothing. |
| epochs | 2 | How many times the model sees every example. With ~400 examples, 2–3 is enough. |
| batch size × grad accumulation | 2 × 4 = 8 | Examples per update. Accumulation adds small batches together to save GPU memory. |
""")
code("""
from unsloth import FastLanguageModel
import torch

MAX_SEQ = 4096
model, tokenizer = FastLanguageModel.from_pretrained(
    model_name="unsloth/Qwen2.5-3B-Instruct-bnb-4bit",   # same family as the agent's qwen2.5:3b
    max_seq_length=MAX_SEQ,
    load_in_4bit=True,
)
model = FastLanguageModel.get_peft_model(
    model,
    r=16, lora_alpha=16, lora_dropout=0,
    target_modules=["q_proj", "k_proj", "v_proj", "o_proj", "gate_proj", "up_proj", "down_proj"],
    use_gradient_checkpointing="unsloth",
    random_state=7,
)
""")
md("""
### Data: same chat template as at use time
Each line of `train.jsonl` is `{"messages": [system, user = facts, assistant = correct JSON]}`,
exactly what `rehearse why` sends to the model. We render it with Qwen's chat template.
""")
code("""
from datasets import load_dataset
from unsloth.chat_templates import get_chat_template

tokenizer = get_chat_template(tokenizer, chat_template="qwen-2.5")
ds = load_dataset("json", data_files={"train": "train.jsonl", "val": "val.jsonl"})

def render(batch):
    return {"text": [tokenizer.apply_chat_template(m, tokenize=False, add_generation_prompt=False)
                     for m in batch["messages"]]}

ds = ds.map(render, batched=True, remove_columns=ds["train"].column_names)
lens = [len(tokenizer(t).input_ids) for t in ds["train"]["text"]]
print(len(ds["train"]), "train,", len(ds["val"]), "val; longest example", max(lens), "tokens")
""")
md("""
### Train only on the answer
We do not want the model to learn to *write facts*; only to *answer* them. `train_on_responses_only`
masks everything before the assistant turn, so the loss counts only the JSON answer.
""")
code("""
from trl import SFTTrainer, SFTConfig
from unsloth.chat_templates import train_on_responses_only

trainer = SFTTrainer(
    model=model,
    tokenizer=tokenizer,
    train_dataset=ds["train"],
    eval_dataset=ds["val"],
    args=SFTConfig(
        dataset_text_field="text",
        max_seq_length=MAX_SEQ,
        per_device_train_batch_size=2,
        gradient_accumulation_steps=4,
        num_train_epochs=2,
        learning_rate=2e-4,
        warmup_steps=5,
        logging_steps=5,
        eval_strategy="steps",
        eval_steps=20,
        optim="adamw_8bit",
        weight_decay=0.01,
        lr_scheduler_type="linear",
        seed=7,
        output_dir="outputs",
        report_to="none",
    ),
)
trainer = train_on_responses_only(
    trainer,
    instruction_part="<|im_start|>user\\n",
    response_part="<|im_start|>assistant\\n",
)
stats = trainer.train()
""")
md("""
### Loss plot: did it learn, did it overfit?
- Training loss should go down.
- Validation loss should go down too, then flatten. If it **rises** while training loss keeps
  falling, stop earlier (fewer epochs) or use a smaller `r`: that is overfitting.
Save the plot as `loss.png` and copy it to `docs/` for the report.
""")
code("""
import matplotlib.pyplot as plt
h = trainer.state.log_history
tr = [(x["step"], x["loss"]) for x in h if "loss" in x]
ev = [(x["step"], x["eval_loss"]) for x in h if "eval_loss" in x]
plt.plot(*zip(*tr), label="training loss")
if ev: plt.plot(*zip(*ev), "o-", label="validation loss")
plt.xlabel("step"); plt.ylabel("loss"); plt.legend(); plt.title("Rehearse LoRA fine-tune")
plt.savefig("loss.png", dpi=150); plt.show()
""")
code("""
# Quick check: does the tuned model answer a validation example in the right JSON format?
import json
FastLanguageModel.for_inference(model)
ex = load_dataset("json", data_files="val.jsonl")["train"][0]["messages"]
inputs = tokenizer.apply_chat_template(ex[:-1], add_generation_prompt=True, return_tensors="pt").to("cuda")
out = model.generate(input_ids=inputs, max_new_tokens=400, temperature=0.0, do_sample=False)
answer = tokenizer.decode(out[0][inputs.shape[1]:], skip_special_tokens=True)
print(answer); print("valid JSON:", bool(json.loads(answer[answer.find("{"):answer.rfind("}")+1])))
print("expected:", ex[-1]["content"])
""")
md("""
### Save the adapter and export GGUF for Ollama
`q4_k_m` is the same quantisation Ollama uses for `qwen2.5:3b` (~1.9 GB). The adapter is saved
first, so a failed export does not lose the training. Download `rehearse-hotel-Q4_K_M.gguf` (plus
`loss.png` and the adapter zip; on Kaggle from the version's Output tab), put the GGUF in
`ml/finetune/outputs/` (git-ignored), then on the laptop:

```bash
ollama create rehearse-hotel -f ml/finetune/Modelfile
ollama run rehearse-hotel
```
""")
code("""
model.save_pretrained("rehearse-hotel-lora"); tokenizer.save_pretrained("rehearse-hotel-lora")
model.save_pretrained_gguf("rehearse-hotel", tokenizer, quantization_method="q4_k_m")
!ls -la rehearse-hotel*
!zip -qr rehearse-hotel-lora.zip rehearse-hotel-lora
results = glob.glob("rehearse-hotel*/*.gguf") + glob.glob("*.gguf") + ["loss.png", "rehearse-hotel-lora.zip"]
if ON_KAGGLE:
    for f in results:   # the Output tab shows files in /kaggle/working
        if os.path.dirname(f):
            shutil.copy(f, ".")
    print("See the Output tab:", results)
else:
    for f in results:
        files.download(f)
""")

nb = {"cells": cells, "metadata": {"accelerator": "GPU", "colab": {"provenance": []},
      "kernelspec": {"display_name": "Python 3", "name": "python3"}, "language_info": {"name": "python"}},
      "nbformat": 4, "nbformat_minor": 0}
json.dump(nb, open(Path(__file__).with_name("train.ipynb"), "w"), indent=1)
print("wrote train.ipynb with", len(cells), "cells")
