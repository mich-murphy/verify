# Gate agent entry point

Read, in order:

1. `SPEC.md`: the authoritative v1 spec. Do not edit it without being asked.
2. `STATUS.md`: which build-order steps are done.
3. `DEVELOPMENT.md`: build, test, conventions, coordination.
4. `docs/coordination/current-session.md`: the active brief, when one is assigned.

Working rules:

- Run `make check` before handing work back, and report what you actually ran.
- Never weaken a check, lint rule, threshold or test to get green. Fix the code.
- Stay inside the files you own when work is split between agents.
- Keep docs short. Update `STATUS.md` when a build-order step changes state.
- Do not commit, push or rewrite history unless asked. The parent reviews and commits.
