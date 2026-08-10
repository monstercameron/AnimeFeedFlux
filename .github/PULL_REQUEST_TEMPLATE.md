<!--
Explain WHY, not what. The diff already says what.
-->

## What this changes, and why

<!-- One paragraph. If it corrects the plan, say what the plan had wrong. -->

## Task and spec

- Task: <!-- e.g. A4-12 -->
- Plan section: <!-- e.g. §9.6 -->
- [ ] `PLAN.md` still matches the implementation. If it does not, this PR corrects it **here**, not
      later — a spec that has drifted still gets trusted.
- [ ] The task's stated check passes. (Code existing is not done.)

## Records updated in this change

See `AGENTS.md` → "What you must update alongside your change". Tick or strike through.

- [ ] `TODOS.md` — task ticked `[x]`, or added/split/`[-]` with a reason
- [ ] `CHANGELOG.md` — under `[Unreleased]`, if observable behaviour, a format, an interface, or a
      default changed. Not for refactors.
- [ ] `DEVLOG.md` — if a decision was **reversed**, research overturned an assumption, a bug was
      nearly shipped, or an approach was abandoned. Write the wrong turn down, not just the
      destination.
- [ ] `PLAN.md` §21 — if this answers an open question, the answer and date are recorded and the
      `OQ-*` task ticked

## Verification

<!-- What you actually ran, and what it proved. "CI will tell me" is not verification. -->

- [ ] `go vet` and the linter pass
- [ ] `go test ./...` passes and needed **no network and no API key** (`RULE-1`)
- [ ] `make validate` clean, if anything touched a renderer or feed output (§5.6)
- [ ] Flow sanity tests (`BF-*`) still pass, if anything touched a path they cover

## Things this repo gets wrong if nobody checks

Tick only what applies; delete the rest.

- [ ] Dates: RSS is RFC 822, Atom/JSON are RFC 3339, and they are never crossed (`RULE-5`)
- [ ] No guid is re-derived anywhere; `item_key` stays opaque (§5.1)
- [ ] Nothing new writes during sampling (§11)
- [ ] No item is backdated (`RULE-7`)
- [ ] Grounded link normalization is symmetric — same function both sides (§9.6)
- [ ] Any write changing published output invalidates the cache, including feed-level writes (§11)
- [ ] Model output passes the allowlist sanitizer before storage (`RULE-3`)
- [ ] No secret in the diff, an image layer, a log line, or a recipe (`RULE-2`)
