---
name: phase
description: Start a Rehearse build phase from docs/plan.md (reads context, checks prerequisites, explains the idea, plans, then builds). Use when Yash types /phase N.
argument-hint: "<phase number 0-11>"
disable-model-invocation: true
---

# Start Phase $ARGUMENTS of Rehearse

Follow these steps in order. Do not skip ahead to building.

## 1. Load the context
- Read `CLAUDE.md` (already loaded) and check the **Progress** and **Current state** sections.
- Read the `## Phase $ARGUMENTS:` section of `docs/plan.md` completely.
- Read every file listed under **Read first** in that section, plus `docs/context/decisions.md` (open items for this phase).
- If the section has an **AIOpsLab link**, open the named files under `reference/AIOpsLab/aiopslab/` that you need.

## 2. Check prerequisites (read-only commands only)
- All earlier phases are ticked in Progress. If not, tell Yash which one is unfinished and ask whether to continue anyway.
- `kubectl config current-context` is `kind-rehearse` and `kubectl get nodes` shows Ready (skip for phases that do not need the cluster). If the cluster is down, show `make cluster` and ask.
- `ollama list` shows the models the phase needs.
- `git status` is clean, or tell Yash what is uncommitted.

## 3. Teach before building
- Explain the phase's **Idea to learn** in simple words with a tiny example from this project (Yash is new to AI).
- Summarise the **Watch out** items and any open decisions. For each open decision give your recommendation and ask Yash to choose (use AskUserQuestion when there are real options).

## 4. Plan
- Present a short step-by-step plan: files to create, commands to run (marking which change the cluster or machine), and how each **Done when** check will be verified.
- Wait for Yash's approval.

## 5. Build
- Follow the phase's **Starting prompt** as the specification, plus `.claude/rules/`.
- Small steps; show every install or cluster-changing command before running it; sudo commands are for Yash to run.
- Leave one or two small, important pieces for Yash to write when the phase prompt asks, and say which.
- Record every new decision in `docs/context/decisions.md` as you make it.

When the build is complete, tell Yash to run `/finish-phase $ARGUMENTS`.
