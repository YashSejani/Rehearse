---
name: finish-phase
description: Close a Rehearse phase - verify its done-checks, write the phase doc, update CLAUDE.md progress and state, log decisions, and propose a commit. Use when Yash types /finish-phase N.
argument-hint: "<phase number 0-11>"
disable-model-invocation: true
---

# Finish Phase $ARGUMENTS of Rehearse

## 1. Verify the done-checks
- Read the **Done when** list in the `## Phase $ARGUMENTS:` section of `docs/plan.md`.
- Check each one for real (run the read-only commands, tests, or ask Yash to confirm the ones only he can see, such as Grafana). Report each as pass / fail with the evidence.
- For Go code: `gofmt -l .` empty, `go vet ./...` and `go test ./...` pass.
- If anything fails, stop and fix it (or ask) before continuing.

## 2. Write the phase doc
- `docs/NN-<topic>.md` (NN = two-digit phase number) exists and explains, in simple words: the idea learned, what we built and why, how to run it, the results (tables for eval phases), and problems we hit.

## 3. Update the project memory
- `docs/plan.md`: tick the phase's **Done when** boxes and add `· DONE <date>` to its heading.
- `CLAUDE.md`: tick the phase in **Progress** with the date; update **Current state** (what exists now, what is next, new tools or versions, Go packages that now exist).
- `docs/context/decisions.md`: every decision made this phase is logged; move resolved open items to "Decided".
- `docs/context/environment.md`: new tools, versions, namespaces, ports.
- If `docs/context/hotelreservation.md` has *(verify)* items that this phase checked, update them.

## 4. Propose the commit
- Show `git status` and a proposed commit message: imperative subject `Phase $ARGUMENTS: <summary>`, then a short bullet list.
- **No Co-Authored-By line and no mention of Claude or Anthropic.**
- Commit and push only after Yash says yes.

## 5. Next step
- Suggest `/quiz $ARGUMENTS`, then `/clear` and `/phase <next>`.
