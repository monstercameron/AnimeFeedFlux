# Security

AnimeFeedFlux is a single-admin feed generator with a public, unauthenticated read plane and an LLM
in the write path. Three properties matter more than anything else, because all three fail
*silently*: **the public plane must never write**, **a published link must never be invented**, and
**one leaked password must not lose everything**.

Everything below is downstream of those.

## Reporting a vulnerability

Open a private security advisory on the repository, or email the maintainer. Please do not open a
public issue for anything affecting a running instance.

Include what you did, what you expected, and what happened. A proof of concept against a local
instance is welcome; please do not test against somebody else's.

Expect an acknowledgement within a few days. This is a personal project, not a vendor with an
on-call rota — the response is best effort, and it is honest about that rather than promising a
window it cannot keep.

## Status

**No code exists yet.** What follows is a threat model and a set of deliberate positions taken in
the design, not claims about a running system. When there is an implementation, these become
assertions backed by tests — several are already written as tasks in `TODOS.md`.

## The threat model

An instance is reachable from the internet. It serves feeds to anyone. It fetches arbitrary URLs its
operator chose, parses whatever comes back, and hands some of that to a language model whose output
is then rendered into XML that third-party software consumes.

Three hostile inputs, then: **the public request**, **the upstream feed**, and **the model's
output**. The model is not an authority here; it is an untrusted input source that happens to be
expensive.

## Deliberate positions

### The public plane cannot write, structurally

The publish plane holds a `*sql.DB` opened `mode=ro`, wrapped in a reader interface with no write
methods. This is not "we were careful not to write" — the code path has no writer. A test asserts
the handle rejects writes, so the claim is verified rather than assumed.

It serves a fixed route set, `GET`/`HEAD` only, with no admin surface reachable from it at all.

### There is no authorization model, so authentication is the entire defense

One admin, one role, everything or nothing. That makes a weak login the whole attack surface, which
is why the login is built properly rather than sketched:

- **argon2id**, not bcrypt — no 72-byte truncation, memory-hard, parameters stored beside the hash
  so they can be raised later.
- **TOTP is mandatory, not optional.** With a single admin on a public droplet, a leaked password
  otherwise loses everything. Used steps are recorded with a primary key so the *database* rejects a
  replay race rather than a check-then-insert in application code.
- The TOTP secret is encrypted at rest with a key derived from the environment, so a stolen database
  file alone is not a second factor.
- Sessions: 256-bit tokens hashed at rest, `__Host-` prefixed, `HttpOnly; Secure; SameSite=Strict`,
  12h absolute and 60m idle, rotated on login.
- Authentication is checked by an interceptor on **every RPC**, not once at the WebSocket upgrade.
  "Authenticated at upgrade, trusted forever" is how long-lived socket apps get this wrong.
- Uniform timing and one generic error message for every failure. A different message for "unknown
  user" and "wrong password" is an oracle.

The admin host is additionally IP-allowlisted at nginx. That is defense in depth, not the control.

### Hallucinated links are prevented structurally, not by prompting

For grounded feeds, upstream sources are fetched first and their URLs normalized once, at fetch
time. Only that candidate set is shown to the model. A published `link` must be **byte-equal** to a
URL in that set — not "similar to", present. An invented URL is unpublishable rather than unlikely.

Both sides of that comparison must be normalized by the same function. If candidates keep their
`utm_*` parameters and the output path strips them, a model faithfully echoing a real article URL
fails the check and gets dropped — a silent failure that looks like the model misbehaving and would
starve the feed while appearing to work.

### Model output is sanitized, then escaped again at render

Generated HTML passes a strict allowlist before storage, and the renderer escapes independently.
This text is authored by a model and rendered inside third-party readers and Slack; it is treated as
untrusted input at every boundary. Fuzzing the sanitizer against an XSS corpus is a planned gate, not
an aspiration.

### Upstream XML parsing is bounded

Body size is capped and entity expansion is disabled. A feed reader that parses arbitrary XML without
disabling entity expansion is one billion-laughs payload away from being someone else's problem.

### Secrets never enter the image or the repository

`SCHEMAFLUX_API_KEY` and `AFF_SECRET_KEY` come from a host `env_file` at mode 0600 — never a
Dockerfile `ENV`, never a literal in compose, never a recipe field, never a log line. An image layer
is readable by anyone who can pull it. A redaction filter on the log writer is a backstop, not the
control.

### The container publishes to loopback only

Docker writes its own DNAT rules ahead of the host firewall chain, so publishing a port to `0.0.0.0`
exposes it **past `ufw`**. That is the single most common way a containerized service ends up
unintentionally internet-facing, and here it would expose the admin plane. The container binds
`127.0.0.1` and nginx is the only thing listening publicly.

### Backups are encrypted and leave the machine

A prior incident on the same droplet is the reason: fourteen verified backups, the source database,
and the key that decrypts them all lived on one volume, so the single event they insured against
took all three. A backup on the same disk defends against `rm`, not against loss.

## Known limitations, stated rather than hidden

- **There is no account recovery by email**, because there is no email infrastructure and one admin.
  Recovery is single-use recovery codes or break-glass over SSH with local database access. The
  recovery page says so plainly rather than leaving a dead end at the worst moment.
- **Published content can be factually wrong.** The system generates claims; it does not verify
  them. The mitigations are narrow claims, citations where available, and a correction mechanism.
  This is an accepted property, not an open bug.
- **Deleting or editing a published item does not reach existing subscribers.** RSS has no
  retraction. Anyone who already fetched it keeps their copy, and Slack keeps its message.
- **The publish plane is unauthenticated by design.** If feeds ever need to be private, that is a
  different design — per-subscriber URL tokens — and it changes the caching model. Tracked as an
  open question, not an oversight.
- **A dependency is young.** SchemaFlux is v1.1.0 and only its OpenAI provider is live-verified.
  That is the provider in use, deliberately.
