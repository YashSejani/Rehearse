# How to work on Rehearse with Claude Code

Setup is done (Phase 0, starter pack copied, `docs/context/aiopslab-for-rehearse.md` written).

## On a new machine or fresh clone
1. Clone AIOpsLab for reference (git-ignored):
       git clone https://github.com/microsoft/AIOpsLab reference/AIOpsLab
2. Follow docs/00-install.md for tools, then `make cluster`.

## Every phase
1. Open Claude Code in the repo root (optional: `/output-style` → Learning).
2. `/clear`, then `/phase N` — reads CLAUDE.md, the phase in docs/plan.md and its context files, teaches the idea, plans, builds.
3. `/finish-phase N` — verifies the done-checks, writes docs/NN-*.md, updates CLAUDE.md progress and decisions, proposes a commit.
4. `/quiz N` — 5 questions to check you understood it.

Instead of `/phase N` you can paste the phase's **Starting prompt** from docs/plan.md.

## Where the project memory lives
- `CLAUDE.md` — loaded every session: goal, architecture, rules, current state, progress.
- `.claude/rules/` — coding rules loaded automatically when Claude touches Go, Python, Kubernetes or safety code.
- `.claude/skills/` — the `/phase`, `/finish-phase` and `/quiz` commands.
- `docs/context/` — decisions, environment, the HotelReservation app, AIOpsLab mapping and background papers.
