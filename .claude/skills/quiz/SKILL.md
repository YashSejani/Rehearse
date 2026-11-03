---
name: quiz
description: Quiz Yash on a Rehearse phase or on the overall architecture to check understanding. Use when Yash types /quiz, /quiz N, or asks to be tested.
argument-hint: "[phase number, or empty for the whole architecture]"
---

# Quiz on Rehearse

Topic: phase "$ARGUMENTS" (if empty, the whole architecture).

- If a phase number was given, base the questions on that phase's **Idea to learn**, **Watch out** and what was actually built (its `docs/NN-*.md` and code). Otherwise cover the whole architecture in `CLAUDE.md` and `docs/context/aiopslab-for-rehearse.md`.
- Ask **5 questions**, one at a time. Wait for each answer before the next question.
- Prefer "why" and "what would break if" questions over definitions. Use concrete examples from this project (HotelReservation services, our faults, our gate).
- After each answer: say what was right, correct what was wrong in simple words, and point to the file that explains it.
- At the end, give a short summary of strong and weak areas and suggest what to reread.
