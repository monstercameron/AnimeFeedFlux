# Deploy readiness: nothing is deployed yet

This is the ordered path from "no droplet, no DNS, no certificates exist" to "a feed is live and
validating," for TODOS.md's `C2` (staging) and `C5` (production deploy) sections. **Nothing
described here has been run against a real host as of this writing** — this document was produced
by reading `scripts/deploy-bootstrap.sh`, `scripts/deploy-release.sh`, `scripts/deploy-verify.sh`,
`deploy/RUNBOOK.md`, `deploy/compose.yaml`, the nginx vhosts, and `.github/workflows/release.yml`
against each of the 15 open `C2-*`/`C5-*` tasks, closing the gaps the scripts could close and
naming the ones they cannot. None of those 15 tasks are ticked by this pass — a task that requires
a real droplet, real DNS, or a real rollback having been performed cannot be honestly marked done
by reading code.

Companion doc: `deploy/RUNBOOK.md` is the reference for operating a host once it exists (a normal
release, rollback, restoring a backup, reading logs). This document is only about getting to that
starting line, plus what each command proves against `TODOS.md`.

---

## Decisions to make before running anything

1. **Droplet count and topology.** Staging and production are separate hosts. `deploy-bootstrap.sh`
   does not parameterise `/opt/animefeedflux`, `/etc/animefeedflux`, or the `animefeedflux-data`
   named volume — running staging mode and production mode against the same box collides on all
   three. Budget for two droplets (or accept staging is a throwaway box you can reprovision).
2. **Droplet size and region.** Not decided anywhere in this repo. `deploy/compose.yaml` caps the
   container at `mem_limit: 512m`, and `PLAN.md` §14 sizes everything for a 1–10 feed target on a
   "2 GB box" — that number comes from the neighbouring services (ArticleFlux, CashFlux,
   earlcameron.com) sharing the production droplet per `deploy/RUNBOOK.md`'s "shape of the system"
   paragraph, not from anything AnimeFeedFlux itself measures. A DigitalOcean region close to
   wherever `SCHEMAFLUX_API_KEY`'s provider is fastest from is a reasonable tie-breaker; nothing
   here depends on a specific region.
3. **The admin allowlist CIDR** — your home IP as a `/32` (or a narrow range if it's genuinely
   dynamic within a known block). Required before `deploy-bootstrap.sh` will install the production
   admin vhost at all (see [The admin vhost placeholder](#the-admin-vhost-placeholder-guard) below).
4. **Hostnames.** `anime.earlcameron.com` / `admin.anime.earlcameron.com` for production,
   `staging.anime.earlcameron.com` for staging — these are what's hardcoded into
   `deploy/nginx/*.conf` and `deploy/animefeedflux.env.example`. Changing them means editing those
   checked-in files, not just DNS.
5. **A DigitalOcean API token / droplet creation path** — outside this repo's scope entirely; no
   script here provisions a droplet, only configures one that already exists and is reachable by
   SSH as a user that can `sudo`.

---

## External prerequisites (nothing in this repo can satisfy these)

| Prerequisite | Needed for | Notes |
|---|---|---|
| A DigitalOcean account + token, droplet(s) created | everything below | not automated anywhere in this repo |
| SSH access to each droplet as a `sudo`-capable user | `deploy-bootstrap.sh`, `deploy-release.sh` | |
| DNS: A/AAAA for `staging.anime.earlcameron.com` → staging droplet IP | `C2-01` | |
| DNS: A/AAAA for `anime.earlcameron.com` and `admin.anime.earlcameron.com` → production droplet IP | `C5-01` | can point at the same droplet |
| Your home IP (or CIDR) for the admin allowlist | `C5-03` | see decision 3 above |
| A `SCHEMAFLUX_API_KEY` | first successful generation run | goes in the env file, never in git |
| GitHub repo secrets `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY`, `DEPLOY_KNOWN_HOSTS` | `C5-09` (push-to-`main` auto-deploy) | `.github/workflows/release.yml`'s deploy job checks `DEPLOY_HOST` and skips (not fails) if unset — safe to leave unconfigured until production exists, but `C5-09` cannot close without them |
| `gh` CLI authenticated (or GitHub UI access) | `scripts/rollback.sh`, `scripts/promote.sh` | both scripts shell out to `gh` |

---

## The two silent failures this design invites

Both are called out in `deploy/RUNBOOK.md` and enforced (not just documented) in the scripts —
verified as part of this pass, still true as of this writing.

### 1. Publishing past `ufw`

`deploy/compose.yaml` binds both container ports with an explicit loopback prefix:

```yaml
ports:
  - "127.0.0.1:9310:9310"   # publish plane
  - "127.0.0.1:9311:9311"   # control plane
```

Docker writes its own `DNAT` rules in the `nat` table's `DOCKER` chain, evaluated **before**
`ufw`'s rules in the `filter` table. A port published as `9311:9311` (no loopback prefix) is
reachable from the public internet even though `ufw status` reports it denied — `ufw` is asked a
question Docker's routing never lets it answer. If these ports are ever changed, the `127.0.0.1:`
prefix has to move with them. Verify on the droplet after any compose change:

```sh
sudo iptables -t nat -L DOCKER -n | grep 931          # destination must read 127.0.0.1:9310/9311
curl -m 5 http://<droplet-public-ip>:9311/             # from OUTSIDE the box — must time out, not respond
```

### 2. The admin vhost placeholder guard

`deploy/nginx/admin.anime.earlcameron.com.conf` as checked into git carries a placeholder CIDR
(`203.0.113.0/24`, a documentation/test-net range) because a real home IP does not belong in a
public repo. `scripts/deploy-bootstrap.sh` **refuses to install or enable this vhost while the
placeholder is the CIDR in effect on the deployed box** — confirmed still true by reading the
current script: it diffs the *deployed* copy in `/etc/nginx`, not the source, and only writes a
real CIDR in when one is passed as an argument or `AFF_ADMIN_CIDR`; passing `0.0.0.0/0` or `::/0`
is refused outright. Without a CIDR, the script prints a `DEFERRED` item and exits non-zero rather
than installing a public admin plane. This is the mechanism behind `C5-03`; `C5-04` (confirm it's
actually unreachable off-allowlist) still has to be performed by hand from a vantage point off the
allowlist — a phone on cellular data, not wifi that might share the home IP — because a script
running from an allowed IP cannot prove the negative.

---

## What the scripts assumed but didn't check — closed this pass

Two real gaps existed in `scripts/deploy-bootstrap.sh` before this pass, both silent-failure
shaped exactly as flagged:

- **No staging vhost existed at all.** `deploy/nginx/` only ever had the two production vhost
  files; `staging.anime.earlcameron.com` had no corresponding nginx config anywhere, so `C2-02`
  could not be executed by the script no matter how DNS and the droplet were set up. Fixed by
  adding `AFF_DEPLOY_MODE=staging` to `deploy-bootstrap.sh`: it generates a publish-only vhost for
  `staging.anime.earlcameron.com` (or `AFF_PUBLIC_DOMAIN` if overridden) on the deployed box by
  re-hostnaming the production publish vhost — no new file was added to `deploy/nginx/` in the
  repo, since a real home IP or hostname belongs on the box, not in git, and staging needs no admin
  vhost at all (skipped explicitly, with a warning explaining why).
- **DNS not resolving yet produced a confusing failure.** The script called `certbot` directly;
  when DNS wasn't live, certbot's own error ("Timeout during connect" or similar) was the first and
  only signal, and reads like a firewall or nginx problem rather than a DNS one. Fixed by adding a
  `dns_resolves()` check before every `certbot` invocation: if the hostname doesn't resolve yet, the
  script now prints an explicit "DNS must point here first" warning and skips the certbot attempt
  entirely rather than letting certbot's generic error stand in for the real cause.

Both changes are idempotent and additive to the existing flow — a bootstrap run with DNS already
live and a CIDR already supplied behaves exactly as before.

### Still-open gaps (not closeable from this repo)

- **Certificate renewal is never itself verified.** `apt-get install certbot python3-certbot-nginx`
  registers certbot's own systemd timer (`certbot.timer`, renews twice daily, no-ops unless a cert
  is within 30 days of expiry) — nothing in these scripts starts, disables, or checks it, because
  it's the certbot package's default behavior, not something this repo manages. Worth a one-time
  `systemctl status certbot.timer` check after first bootstrap, since "renewing" (`C2-03`) is a
  claim about time passing, not something a single script run can prove.
- **`C5-04` (admin unreachable off-allowlist)** and **`C5-08` (an actual rollback performed)** and
  **`C5-09` (push-to-`main` reaches the service with no manual step)** are, by their own wording,
  drills — they require a live host and cannot be satisfied by a script run in advance. They're
  sequenced below at the point they become possible to attempt.

---

## Ordered sequence

Each step names the `C2-*`/`C5-*` task(s) it satisfies or unblocks.

### Staging

1. Provision a staging droplet; point `staging.anime.earlcameron.com` at its IP. **(`C2-01`)**
2. `ssh` in, get a checkout of this repo, then:
   ```sh
   sudo AFF_DEPLOY_MODE=staging sh scripts/deploy-bootstrap.sh
   ```
   Installs Docker, the publish-only nginx vhost, and the TLS certificate. **(`C2-02`, `C2-03`)**
   Re-run after fixing whatever it reports `DEFERRED` (most likely: DNS wasn't live yet).
3. `sudo -e /etc/animefeedflux/animefeedflux.env` — fill in `AFF_SECRET_KEY` and
   `SCHEMAFLUX_API_KEY` at minimum (see `deploy/RUNBOOK.md`'s "First deploy" section).
4. Get an image tag — either from a CI build once `C1` exists, or built and pushed manually — then:
   ```sh
   sudo sh scripts/deploy-release.sh sha-abc1234
   ```
   Blocks until the container reports healthy. **(`C2-07`, first half)**
5. From a laptop or CI, not the droplet:
   ```sh
   AFF_VERIFY_BASE_URL=https://staging.anime.earlcameron.com sh scripts/deploy-verify.sh
   ```
   Confirms feeds are publicly fetchable, conditional GET works, and runs `affvalidate` against the
   **live** bytes. **(`C2-07` second half, `C2-08`)**

(`C3` — subscribing a private Slack workspace to the staging URL and running its 11-item proof
list — comes next per `PLAN.md`'s phase order, but is outside this document's `C2`/`C5` scope.)

### Production

6. Provision the production droplet (or reuse the one hosting ArticleFlux/CashFlux/earlcameron.com
   per `deploy/RUNBOOK.md`'s "shape of the system"); point both `anime.earlcameron.com` and
   `admin.anime.earlcameron.com` at it. **(`C5-01`)**
7. ```sh
   sudo sh scripts/deploy-bootstrap.sh <your-home-ip>/32
   ```
   Installs Docker, both nginx vhosts (public unconditionally, admin only because a real CIDR was
   given — see [the placeholder guard](#2-the-admin-vhost-placeholder-guard)), and both TLS
   certificates. **(`C5-02`, `C5-03`)** Re-run after fixing any `DEFERRED` item.
8. From off the allowlist (phone on cellular, not shared wifi) confirm the admin vhost is
   unreachable, and from the allowlisted IP confirm password + TOTP works once an admin account
   exists (step 11). **(`C5-04`)** — see the iptables/curl commands under
   [Publishing past `ufw`](#1-publishing-past-ufw) for the port-level half of this check.
9. `sudo -e /etc/animefeedflux/animefeedflux.env` with real production secrets, at the `0600` mode
   `deploy-bootstrap.sh` already set. **(`C5-06`)**
10. ```sh
    sudo sh scripts/deploy-release.sh sha-abc1234
    ```
    Refuses `latest` outright (`deploy-release.sh` `case "$TAG" in latest) die ...`) — the pinned
    `sha-` tag requirement is enforced, not just documented. **(`C5-05`, half of `C5-07`)**
11. `docker compose ... exec animefeedflux aff admin init` — creates the one admin account.
12. `sh scripts/deploy-verify.sh` against the default `https://anime.earlcameron.com`. **(rest of
    `C5-07`)**
13. **Perform an actual rollback**, not a dry run: deploy a second tag, confirm it's live, then
    ```sh
    sh scripts/rollback.sh          # or: sh scripts/rollback.sh <first-tag>
    ```
    and re-run `deploy-verify.sh` to confirm the rollback served the right content, not just that
    the healthcheck passed. **(`C5-08`)** — `deploy-release.sh` records `.previous-tag` on every
    release specifically so this has something to target without guessing.
14. Set the GitHub repo secrets (`DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY`,
    `DEPLOY_KNOWN_HOSTS`), push a trivial commit to `main`, and confirm `release.yml`'s deploy job
    runs (not skips) and the service updates with no manual step. **(`C5-09`)**
15. Repoint the Slack workspace from `C3` at the production feed URLs and confirm continuity over a
    few days — no gap, no duplicate burst at the cutover. **(`C5-10`)**

---

## Task-by-task status

| Task | Mechanism | Status after this pass |
|---|---|---|
| `C2-01` DNS for staging | external (registrar) | prerequisite, not scriptable |
| `C2-02` nginx vhost for staging | `deploy-bootstrap.sh` `AFF_DEPLOY_MODE=staging` | **gap closed this pass** — previously no staging vhost existed at all |
| `C2-03` TLS cert, staging | same, plus new `dns_resolves()` pre-check | **gap closed this pass** for the confusing-failure mode; renewal itself unverified (certbot's own timer) |
| `C2-07` Deploy + confirm fetchable | `deploy-release.sh` + `deploy-verify.sh` | scripts already covered this; needs a real host to run against |
| `C2-08` External validator against live staging | `deploy-verify.sh` (`AFF_VERIFY_BASE_URL`) | already covered, verified this pass |
| `C5-01` DNS for production | external (registrar) | prerequisite, not scriptable |
| `C5-02` nginx vhosts + TLS, both | `deploy-bootstrap.sh` production mode | already covered; DNS pre-check now added |
| `C5-03` Admin IP allowlist | `deploy-bootstrap.sh` CIDR arg + placeholder guard | already covered, verified robust this pass |
| `C5-04` Confirm admin unreachable off-allowlist | manual, off-network vantage point | not automatable; commands documented |
| `C5-05` Compose pins `sha-`, never `latest` | `deploy-release.sh` refuses `latest` | already enforced, verified this pass |
| `C5-06` env_file real secrets at 0600 | `deploy-bootstrap.sh` seeds + chmods; human fills real values | already covered |
| `C5-07` First production deploy, feeds live | `deploy-release.sh` + `deploy-verify.sh` | already covered; needs a real host |
| `C5-08` Actual rollback performed | `rollback.sh` (untouched) + `.previous-tag` | mechanism exists; the drill itself has not been performed — cannot be, nothing is deployed |
| `C5-09` Push to `main` auto-deploys | `.github/workflows/release.yml` (untouched) + repo secrets | blocked on secrets being configured, external to this repo |
| `C5-10` Slack pointed at production | manual, depends on `C3` | not scriptable |
