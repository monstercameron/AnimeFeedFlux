# CLAUDE.md

**`AGENTS.md` is canonical — read it first**, and follow where it points. This file exists only
because Claude Code loads `CLAUDE.md` by name; it deliberately duplicates nothing, per the
no-duplicated-facts rule in `CONTRIBUTING.md`.

Three reminders that are cheap to state and expensive to forget:

- **Check what exists before planning against it.** This repository is currently a specification
  and a build order, with no code. The plan describes the destination, not the present.
- **Core engine first, UI last.** Phase order in `PLAN.md` §18 is a dependency order. Do not open
  Phase D early.
- **Verify before claiming done.** The default test run makes no network calls by design, so it
  proves nothing about the provider; `-race` runs only in CI; feed correctness needs
  `make validate`. See `AGENTS.md` → "Verifying your work".
- **Update the records in the same change.** Ticking `TODOS.md`, and a `CHANGELOG.md` or `DEVLOG.md`
  entry where the triggers apply, is part of finishing — see `AGENTS.md` → "What you must update
  alongside your change".
