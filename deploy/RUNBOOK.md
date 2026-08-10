# AnimeFeedFlux — Operations Runbook

For the person on call at 2am who did not build this. If you only read one
section, read [First deploy] if nothing is running yet, or [When a feed goes
stale] if that's why you're here.

Related design docs, in case something here doesn't match reality:
`PLAN.md` §2 (two planes), §4 (security/authn), §15 (operations), §19
(definition of done). This file describes *how to operate what §15
describes*; it doesn't repeat the reasoning, only the commands.

Looking for the very first setup instead — fresh clone to a working feed, no droplet or DNS
required? That's `docs/first-run.md`, not this file. This runbook picks up from "there is a deployed
host"; first-run.md covers getting the admin account, first feed, and first published item to exist
at all, including on a laptop.

Looking for what has to exist *before* any command below can run at all — a DigitalOcean token, DNS
records, the decisions (droplet size, region, admin allowlist CIDR) an operator has to make, and
which of TODOS.md's C2/C5 tasks each command actually satisfies? That's `docs/deploy-readiness.md`.
Nothing in this repo is deployed as of this writing; that doc is the ordered path from nothing to a
live feed, this file is the reference for operating what's already live.

## The shape of the system, in one paragraph

One container (`animefeedflux`), image `ghcr.io/monstercameron/animefeedflux`,
runs on the droplet bound to `127.0.0.1:9310` (publish) and `127.0.0.1:9311`
(admin) — **never** on `0.0.0.0`, because Docker's DNAT rules are written
ahead of the host `ufw` chain, so a `0.0.0.0` bind would put the admin plane
on the public internet regardless of what `ufw` says. nginx is the only thing
listening publicly, on ports 80/443, terminating TLS for two vhosts:
`anime.earlcameron.com` (public feeds, no allowlist) and
`admin.anime.earlcameron.com` (control plane, IP-allowlisted **and** behind
password+TOTP). One SQLite database on a named Docker volume
(`animefeedflux-data`) is the only copy of everything ever generated.

---

## First deploy

Prerequisites: DNS for both hostnames already points at the droplet, and you
have SSH access as a user that can `sudo`.

```sh
# on the droplet, from a checkout of this repo (any branch — bootstrap
# only reads deploy/, it does not build anything)
sudo sh scripts/deploy-bootstrap.sh <your-home-ip>/32
```

This installs Docker + the compose plugin, lays down `/opt/animefeedflux`
and `/etc/animefeedflux`, creates the named volume, installs both nginx
vhosts, and obtains TLS certificates. It is **idempotent** — the first run on
a real box commonly half-fails (DNS not propagated yet, certbot rate-limited,
whatever), and re-running it after fixing the blocker is not just supported,
it's the expected workflow. It ends with a summary of anything still
outstanding and exits non-zero if something is.

If you don't pass a home IP (or set `AFF_ADMIN_CIDR`), the admin vhost is
**skipped entirely** rather than installed with the placeholder allowlist —
see [Why the admin vhost sometimes refuses to install] below. Re-run with the
IP once you have it.

After bootstrap reports clean:

1. Fill in real secrets:
   ```sh
   sudo -e /etc/animefeedflux/animefeedflux.env
   ```
   At minimum: `AFF_SECRET_KEY` (`openssl rand -base64 48`) and
   `SCHEMAFLUX_API_KEY`. See `deploy/animefeedflux.env.example` for the full
   list and what each does.

   If you also set `AFF_PASSWORD_PEPPER` (optional, PLAN.md §4): copy it
   somewhere that is **not** the database backup, and not the same place as
   `AFF_BACKUP_ENCRYPTION_KEY` if you use one of those too. Losing the pepper
   with no other copy locks out the one admin account permanently — there is
   no recovery path that doesn't involve this exact string. See the warning
   in `deploy/animefeedflux.env.example` before setting it.

2. Deploy an actual image. If CI has already built one, grab its tag from the
   GHCR package page or a recent `sha-<commit>`; otherwise push to `main` and
   let CI build it, then:
   ```sh
   sudo sh scripts/deploy-release.sh sha-abc1234
   ```
   This writes the tag into `/opt/animefeedflux/compose.yaml`, pulls, brings
   the container up, and **blocks until the healthcheck passes**, failing
   loudly (non-zero exit, last 50 log lines printed) if it never does.

3. Create the admin account (there is no default password):
   ```sh
   docker compose -f /opt/animefeedflux/compose.yaml exec animefeedflux \
       aff admin init
   ```
   *(If `aff admin init` isn't wired up yet in the running binary, this is
   the moment you'll discover it — check `cmd/animefeedflux` for the current
   subcommand name before assuming this runbook is stale.)*

4. Verify it's not just alive but correct:
   ```sh
   sh scripts/deploy-verify.sh
   ```
   Run this from your laptop or CI, not the droplet — it's checking the
   public HTTPS path a real subscriber uses.

5. Confirm the admin plane is actually unreachable from off the allowlist
   (try from a phone on cellular data, not wifi that might share the home
   IP) and reachable from the allowlisted IP with password + TOTP.

---

## Staging (C2)

Staging exists for one reason: Slack polls over public TLS, so proving the Slack integration (C3)
needs a real reachable HTTPS host before production is on the line. It is publish-plane only — no
admin vhost, no allowlist to configure — and it should be a **separate droplet from production**:
`deploy-bootstrap.sh` does not parameterise `/opt/animefeedflux`, `/etc/animefeedflux`, or the
`animefeedflux-data` volume name, so running staging mode and production mode against the same box
would collide on all three.

```sh
# on a staging droplet, DNS for staging.anime.earlcameron.com already pointing at it
sudo AFF_DEPLOY_MODE=staging sh scripts/deploy-bootstrap.sh
```

This generates a publish-only nginx vhost for `staging.anime.earlcameron.com` (the production
publish vhost as committed, re-hostnamed on the deployed box — there is no separate
`deploy/nginx/staging.*.conf` in the repo) and obtains its certificate. Override the hostname with
`AFF_PUBLIC_DOMAIN` if staging should live somewhere else. Everything after that is identical to
[First deploy]: fill in `/etc/animefeedflux/animefeedflux.env`, run `deploy-release.sh` with an
image tag, then verify:

```sh
AFF_VERIFY_BASE_URL=https://staging.anime.earlcameron.com sh scripts/deploy-verify.sh
```

---

## A normal release

This is what CI's `release.yml` does on every push to `main` (build → test →
`make validate` → push image → deploy → health-gate → verify `/healthz`).
`scripts/deploy-release.sh` on the droplet is the piece that does the actual
"write the tag, pull, wait for healthy" work — the workflow currently inlines
an equivalent sequence directly over SSH rather than calling this script, so
if you're reading the workflow file and it looks slightly different from this
script, that's why; they should stay behaviorally equivalent.

Manual release (e.g. deploying a tag CI hasn't auto-deployed, or re-running a
release that timed out on the healthcheck for an unrelated reason):

```sh
ssh <deploy-user>@<droplet> \
    'sh /opt/animefeedflux-src/scripts/deploy-release.sh sha-abc1234'
```

(Adjust the source path — wherever a checkout with `scripts/` lives on the
box. The script only needs `compose.yaml` in the app dir and Docker; it does
not need a full repo checkout to run, but it has to be invoked from
somewhere.)

Then confirm end-to-end:

```sh
sh scripts/deploy-verify.sh
```

**What "success" looks like:** `deploy-release.sh` exits 0 and printed
`deploy of <tag> complete`. Anything else — including a plain `docker compose
up -d` that returned 0 on its own — is not sufficient proof the release
worked; that's the entire reason this script exists instead of just running
compose directly.

---

## Rollback

Rollback is `scripts/rollback.sh` (unmodified by this work — read it, don't
edit it). It does **not** touch `main`; it re-triggers the release workflow
with an explicit `image_tag` input, which redeploys an existing immutable
`sha-` tag. That's the whole reason tags are immutable and production never
pins `latest`: putting the previous tag back is always possible.

```sh
sh scripts/rollback.sh              # roll back to whatever ran before the current tag
sh scripts/rollback.sh sha-abc1234  # roll back to a specific tag
sh scripts/rollback.sh --list       # see recent tags and recent commits on main
```

If GitHub Actions is unreachable (outage, no `gh` auth) and this needs to
happen from the droplet directly:

```sh
ssh <deploy-user>@<droplet>
cat /opt/animefeedflux/.previous-tag     # what deploy-release.sh recorded last time
sh /opt/animefeedflux-src/scripts/deploy-release.sh <that-tag>
```

After either path: `main` still points at the bad commit. Fix forward on
`dev`, run `scripts/promote.sh`, and don't leave production and `main`
disagreeing for long — the next promotion redeploys whatever `main` says,
which would silently re-introduce the bug you just rolled back.

Verify afterward with `sh scripts/deploy-verify.sh` — a rollback that
"succeeded" per the healthcheck but serves the wrong content is still a
broken rollback.

---

## Restoring a backup

**Restoring the database is not restoring the deployment.** The DB backup
contains no secrets — `AFF_SECRET_KEY`, `SCHEMAFLUX_API_KEY`,
`AFF_PASSWORD_PEPPER` (if set), and `AFF_BACKUP_ENCRYPTION_KEY` (if set) all
live only in `/etc/animefeedflux/animefeedflux.env` on the host, deliberately
never in SQLite. A restored DB with a lost or mismatched pepper does not
verify the admin password even though the hash is correct — that is the
pepper working as designed, not a restore bug. Confirm the env file's
secrets are the ones the backup was taken under before assuming a restore
that "completed" actually leaves the admin account usable.

Backups are nightly `VACUUM INTO` snapshots (in-process, not a host cron —
see PLAN.md §15.4) written to `AFF_BACKUP_DIR`
(`/var/lib/animefeedflux/backups` inside the container, i.e. on the
`animefeedflux-data` volume) and, per §15, shipped encrypted off the box.
Restoring **into the live volume is a last resort** — prefer a scratch
instance first, both because that's what §19's "a backup has been restored"
criterion actually tests and because it lets you confirm the backup is good
*before* you've destroyed the current (possibly still-partially-good) state.

**Scratch restore (preferred, and the drill C4-06 / §19.8 requires at least
once):**

```sh
# on any Docker host, does not have to be the droplet
docker volume create animefeedflux-restore-test
docker run --rm -v animefeedflux-restore-test:/data -v /path/to/backup:/backup:ro \
    gcr.io/distroless/static:nonroot cp /backup/animefeedflux-<date>.db /data/animefeedflux.db
# then run the image against that volume, on a scratch port, and confirm
# feeds render identically to what you expect. Do NOT point it at any
# production hostname.
```

**Live restore (only if the current database is actually gone or corrupt —
this is destructive):**

```sh
ssh <deploy-user>@<droplet>
docker compose -f /opt/animefeedflux/compose.yaml down
docker run --rm -v animefeedflux-data:/data -v /path/to/decrypted/backup:/backup:ro \
    gcr.io/distroless/static:nonroot cp /backup/animefeedflux-<date>.db /data/animefeedflux.db
docker compose -f /opt/animefeedflux/compose.yaml up -d
```

Because the backup is a `VACUUM INTO` snapshot (not a raw file copy — WAL
means a raw copy of `.db`/`-wal`/`-shm` at three different instants is
corrupt by construction), there's no separate `-wal`/`-shm` to restore
alongside it; the container creates fresh ones on boot. After either restore
path, run `sh scripts/deploy-verify.sh` and specifically check that item
counts / recent items in the admin UI or `/healthz` look like the point in
time the backup was taken from, not silently empty or silently stale.

---

## Rotating the provider key

`SCHEMAFLUX_API_KEY` (never `OPENAI_API_KEY` — see PLAN.md §8, §16) lives
only in `/etc/animefeedflux/animefeedflux.env` at `0600`, never in the
database, never in a recipe, never in an image layer.

```sh
ssh <deploy-user>@<droplet>
sudo -e /etc/animefeedflux/animefeedflux.env      # replace SCHEMAFLUX_API_KEY
docker compose -f /opt/animefeedflux/compose.yaml up -d --force-recreate
```

`up -d --force-recreate` (not just `restart`) matters here: `env_file` values
are read at container creation, so a plain restart of the existing container
keeps the OLD key in the running process's environment. Confirm the new key
took by watching the next scheduled generation run succeed
(`docker logs animefeedflux`, look for a `run.finished` line with
`outcome: success`) rather than assuming the recreate alone proves it.

Revoke the old key at the provider console only after you've confirmed the
new one works — revoking first and confirming second means the confirmation
step tells you nothing if it fails (was it the new key, or the revoked old
one still cached somewhere?).

---

## When a feed goes stale

"Stale" here means: an enabled feed's last successful run is older than its
schedule plus the configured grace factor. The watchdog (§15) flags this on
`/healthz` and, if `AFF_SLACK_WEBHOOK_URL` is set, posts to Slack — that's
usually how you find out, not by noticing the feed looks thin.

1. **Confirm it, don't just trust the alert:**
   ```sh
   curl -s https://anime.earlcameron.com/healthz | jq .
   ```
   Look at the per-feed staleness age and error count for the specific slug.

2. **Read what actually happened** — `docker logs`, not `journalctl` (see
   [docker logs vs journalctl] below):
   ```sh
   docker logs --since 24h animefeedflux | grep '"feed_slug":"<slug>"'
   ```
   Look for the canonical `run.finished` line for that feed and its
   `outcome`/`reason` fields (§15.0 — one line per run, not scattered
   progress logs). Common causes, roughly in order of likelihood:
   - `outcome: failed`, `reason` naming a provider error — check
     `SCHEMAFLUX_API_KEY` is valid and the provider isn't down.
   - No `run.finished` line at all for the expected window — the scheduler
     isn't firing. Check `AFF_GENERATION_ENABLED` (the kill switch) isn't
     `0`, and check the container didn't restart at the scheduled time
     (`docker ps` uptime vs. the schedule).
   - `outcome: rejected` repeatedly — validation or novelty is rejecting
     every candidate. This is a content-quality problem, not an
     infrastructure one; look at the reject reasons in `/history` via the
     admin UI.

3. **Check the container itself is healthy**, independent of any one feed:
   ```sh
   docker inspect -f '{{.State.Health.Status}}' animefeedflux
   docker stats --no-stream animefeedflux    # OOM-adjacent? mem_limit is 512m
   ```

4. If generation is simply behind (transient provider blip, one missed
   schedule) it usually self-heals on the next scheduled run — the watchdog
   clears once a run succeeds. If it's been down long enough that subscribers
   would notice, trigger a manual run from the admin UI (`/generate`) rather
   than waiting.

---

## `docker logs` vs `journalctl` — the two-places-to-look problem

This is a deliberate, accepted trade documented in PLAN.md §15: containerizing
this one service means it now logs somewhere the other three services don't.

| Service | Where its logs live |
|---|---|
| `animefeedflux` | `docker logs animefeedflux` (json-file driver, stdout only — the app must never write its own log files) |
| `articleflux`, `cashflux`, `earlcameron` | `journalctl -u <service>` (systemd units) |
| nginx (fronts all four) | `/var/log/nginx/*-access.log` / `*-error.log`, per-vhost |

If you're chasing a request that touched nginx and then AnimeFeedFlux, you
need **both**: nginx's access log to confirm the request arrived and what
nginx did with it (rate-limited? proxied? which upstream?), and `docker logs`
for what the app did with it. There is no unified view — do not waste time
looking for AnimeFeedFlux lines in `journalctl`, they are not there and never
will be as long as it runs in Docker.

```sh
docker logs -f --tail=100 animefeedflux              # follow
docker logs --since 1h animefeedflux                 # recent window
journalctl -u articleflux -f                          # a neighbour, for comparison
tail -f /var/log/nginx/animefeedflux-error.log
```

---

## Why the admin vhost sometimes refuses to install

`deploy/nginx/admin.anime.earlcameron.com.conf` as checked into git carries a
placeholder allowlist CIDR (`203.0.113.0/24` — a documentation/test-net
range, not anyone's real IP) because a real home IP does not belong in a
public repository. `deploy-bootstrap.sh` will not install (or re-enable) this
vhost while that placeholder is the CIDR in effect on the box: doing so would
make the admin control plane — password, TOTP, every mutating RPC — reachable
from anywhere the moment nginx reloads, which is the single mistake this
design most invites and the one nginx's own file comment calls out as "the
one mistake this file exists to prevent."

Fix: re-run bootstrap with the real IP.
```sh
sudo sh scripts/deploy-bootstrap.sh 203.0.113.7/32
```
If bootstrap reports the admin vhost as already installed with "a real
allowlist CIDR" but you still can't reach it, or CAN reach it from somewhere
you shouldn't be able to, check `/etc/nginx/sites-available/admin.anime.earlcameron.com.conf`
directly — someone may have hand-edited it since.

---

## The other footgun this design invites: publishing past `ufw`

`deploy/compose.yaml` binds both container ports as `127.0.0.1:9310:9310` and
`127.0.0.1:9311:9311` — never `9311:9311` or `0.0.0.0:9311:9311`. This is not
a style preference. Docker manages its own iptables `DNAT` rules in a chain
that is evaluated **before** `ufw`'s rules, so a container publishing a port
without a loopback prefix is reachable from the internet **even if `ufw`
shows that port as denied**. `ufw status` lying to you about what's actually
reachable is exactly the failure mode.

If you ever need to change these ports (new port number, a second instance,
whatever), keep the `127.0.0.1:` prefix. To sanity-check after any compose
change:

```sh
sudo iptables -t nat -L DOCKER -n | grep 931   # should show 127.0.0.1:9310/9311 as the destination, not 0.0.0.0
curl -m 5 http://<droplet-public-ip>:9311/     # from OUTSIDE the box — must time out / refuse, not respond
```

---

## Quick reference

| Task | Command |
|---|---|
| First-time host setup (production) | `sudo sh scripts/deploy-bootstrap.sh <home-ip>/32` |
| First-time host setup (staging) | `sudo AFF_DEPLOY_MODE=staging sh scripts/deploy-bootstrap.sh` |
| Deploy/update a tag | `sh scripts/deploy-release.sh <tag>` (on the droplet) |
| Verify a deploy | `sh scripts/deploy-verify.sh` |
| Rollback | `sh scripts/rollback.sh [tag \| --list]` |
| Promote dev → main | `sh scripts/promote.sh` (this IS a deploy) |
| App logs | `docker logs -f animefeedflux` |
| Neighbour service logs | `journalctl -u <articleflux\|cashflux\|earlcameron> -f` |
| nginx logs | `/var/log/nginx/animefeedflux*-{access,error}.log` |
| Health | `curl -s https://anime.earlcameron.com/healthz \| jq .` |
| Container health state | `docker inspect -f '{{.State.Health.Status}}' animefeedflux` |
