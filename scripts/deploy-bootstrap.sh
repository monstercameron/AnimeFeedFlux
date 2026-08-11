#!/bin/sh
# deploy-bootstrap.sh — first-time host setup for AnimeFeedFlux (TODOS C1/C5,
# PLAN.md §15.1-15.4, §19).
#
# Run ON THE DROPLET, as root or via sudo:
#
#   sudo sh scripts/deploy-bootstrap.sh [admin-cidr]
#
# admin-cidr (optional) is the home-IP allowlist for the control plane, e.g.
# 203.0.113.7/32. It can also be supplied as AFF_ADMIN_CIDR. Without it, every
# step that does not touch the admin vhost still runs; the admin vhost install
# is skipped and the script exits non-zero so a bootstrap run is never
# silently incomplete.
#
# Staging (TODOS.md C2, PLAN.md §18 C2) is publish-plane only — no admin
# vhost, no allowlist to configure. Run on a SEPARATE droplet from production
# (this script does not parameterise APP_DIR/CONF_DIR/the named volume, so
# running both modes against the same box would collide on all three):
#
#   sudo AFF_DEPLOY_MODE=staging sh scripts/deploy-bootstrap.sh
#
# Defaults to staging.anime.earlcameron.com; override with AFF_PUBLIC_DOMAIN
# if staging should live at a different hostname.
#
# IDEMPOTENT ON PURPOSE. The first run on a real box always half-fails —
# apt hiccups, DNS for a hostname isn't live yet so certbot can't issue,
# whatever — and re-running this script must converge, not compound the
# damage (double-install packages, clobber an operator-edited env file,
# re-obtain certificates that already exist). Every step below checks before
# it acts.
#
# What this does NOT do: touch compose.yaml's image tag (that is
# deploy-release.sh's job, run afterwards) or write real secrets into the env
# file (that is a manual `sudo -e`, because a secret typed into a script
# argument ends up in shell history and process listings).

set -eu

# --- 0. preflight ----------------------------------------------------------

if [ "$(id -u)" -ne 0 ]; then
    echo "must run as root (sudo sh scripts/deploy-bootstrap.sh ...)" >&2
    exit 1
fi

# Resolve paths relative to this script so it can be invoked from anywhere,
# e.g. `sudo sh /opt/animefeedflux-src/scripts/deploy-bootstrap.sh`.
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
DEPLOY_DIR="$REPO_ROOT/deploy"

APP_DIR=/opt/animefeedflux
CONF_DIR=/etc/animefeedflux
ENV_FILE="$CONF_DIR/animefeedflux.env"
VOLUME_NAME=animefeedflux-data

ADMIN_CIDR="${1:-${AFF_ADMIN_CIDR:-}}"
PLACEHOLDER_CIDR="203.0.113.0/24"

DEPLOY_MODE="${AFF_DEPLOY_MODE:-production}"
case "$DEPLOY_MODE" in
    production) DEFAULT_DOMAIN="anime.earlcameron.com" ;;
    staging)    DEFAULT_DOMAIN="staging.anime.earlcameron.com" ;;
    *)
        echo "AFF_DEPLOY_MODE must be 'production' or 'staging' (got '$DEPLOY_MODE')" >&2
        exit 1
        ;;
esac
PUBLIC_DOMAIN="${AFF_PUBLIC_DOMAIN:-$DEFAULT_DOMAIN}"

step() { printf '\n==> %s\n' "$1"; }
done_() { printf '    done: %s\n' "$1"; }
skip() { printf '    skip: %s (already done)\n' "$1"; }
warn() { printf '    WARN: %s\n' "$1" >&2; }

DEFERRED=""

# --- 1. Docker + compose plugin --------------------------------------------

step "Docker engine and compose plugin"
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    skip "docker + compose plugin present ($(docker --version))"
else
    # Official convenience script installs both docker-ce and the compose
    # plugin from Docker's own apt repo, which is what keeps the compose v2
    # syntax (`docker compose`, no hyphen) this whole pipeline assumes.
    apt-get update -y
    apt-get install -y ca-certificates curl
    install -m 0755 -d /etc/apt/keyrings
    if [ ! -f /etc/apt/keyrings/docker.asc ]; then
        curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
        chmod a+r /etc/apt/keyrings/docker.asc
    fi
    if [ ! -f /etc/apt/sources.list.d/docker.list ]; then
        . /etc/os-release
        echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" \
            > /etc/apt/sources.list.d/docker.list
    fi
    apt-get update -y
    apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    systemctl enable --now docker
    done_ "docker $(docker --version) + $(docker compose version)"
fi

# --- 2. app + config directories -------------------------------------------

step "/opt/animefeedflux"
install -d -m 0755 "$APP_DIR"
if [ -f "$APP_DIR/compose.yaml" ] && cmp -s "$DEPLOY_DIR/compose.yaml" "$APP_DIR/compose.yaml"; then
    skip "compose.yaml already up to date"
else
    # deploy-release.sh rewrites the image tag IN this copy on every release,
    # so re-copying the repo's compose.yaml on a re-run would stomp whatever
    # tag production is currently pinned to. Only copy if it's genuinely
    # missing or differs in something other than the image line — warn rather
    # than overwrite blindly.
    if [ -f "$APP_DIR/compose.yaml" ]; then
        # No process substitution — this must run under plain POSIX sh/dash,
        # not just bash. Strip the image: line to two temp files and diff those.
        tmp_a=$(mktemp)
        tmp_b=$(mktemp)
        grep -v '^[[:space:]]*image:' "$DEPLOY_DIR/compose.yaml" > "$tmp_a"
        grep -v '^[[:space:]]*image:' "$APP_DIR/compose.yaml" > "$tmp_b"
        if diff -q "$tmp_a" "$tmp_b" >/dev/null 2>&1; then
            skip "compose.yaml differs only by the pinned image tag — leaving it alone"
        else
            warn "$APP_DIR/compose.yaml differs from deploy/compose.yaml beyond the image tag."
            warn "Not overwriting a file the release process may have customised. Diff and merge by hand:"
            warn "  diff $APP_DIR/compose.yaml $DEPLOY_DIR/compose.yaml"
            DEFERRED="$DEFERRED compose.yaml-review"
        fi
        rm -f "$tmp_a" "$tmp_b"
    else
        install -m 0644 "$DEPLOY_DIR/compose.yaml" "$APP_DIR/compose.yaml"
        done_ "installed compose.yaml"
    fi
fi

step "$CONF_DIR (secrets, 0600)"
install -d -m 0700 "$CONF_DIR"
if [ -f "$ENV_FILE" ]; then
    skip "$ENV_FILE already exists — not touching real secrets"
else
    # Seed from the example so every key is present, then chmod immediately.
    # The window between create and chmod is why install -m does both in one
    # syscall rather than touch-then-chmod.
    install -m 0600 "$DEPLOY_DIR/animefeedflux.env.example" "$ENV_FILE"
    done_ "seeded $ENV_FILE from the example — placeholders only"
    warn "$ENV_FILE still holds placeholder secrets. Edit it now:"
    warn "  sudo -e $ENV_FILE"
    DEFERRED="$DEFERRED env-secrets"
fi
# Re-assert mode/ownership every run regardless — a stray chmod by a human
# during an incident should not survive the next bootstrap.
chmod 0600 "$ENV_FILE"
chown root:root "$ENV_FILE"

# --- 3. named volume ---------------------------------------------------

step "named volume $VOLUME_NAME"
if docker volume inspect "$VOLUME_NAME" >/dev/null 2>&1; then
    skip "volume exists"
else
    docker volume create "$VOLUME_NAME" >/dev/null
    done_ "created volume $VOLUME_NAME"
fi

# --- 4. nginx vhosts ---------------------------------------------------

step "nginx"
if command -v nginx >/dev/null 2>&1; then
    skip "nginx already installed"
else
    apt-get install -y nginx
    done_ "installed nginx"
fi
install -d -m 0755 /var/www/certbot

install_vhost() {
    src="$1"
    name="$(basename "$src")"
    dest="/etc/nginx/sites-available/$name"
    if [ -f "$dest" ]; then
        skip "$name already installed at $dest"
    else
        install -m 0644 "$src" "$dest"
        done_ "installed $dest"
    fi
    if [ ! -e "/etc/nginx/sites-enabled/$name" ]; then
        ln -s "$dest" "/etc/nginx/sites-enabled/$name"
        done_ "enabled $name"
    else
        skip "$name already enabled"
    fi
}

if [ "$DEPLOY_MODE" = "staging" ]; then
    # No dedicated deploy/nginx/staging.*.conf is checked in — the production
    # publish vhost as committed is the whole template a staging vhost needs
    # (same proxy_pass target, same conditional-GET passthrough), it just
    # needs a different server_name and cert path. Generate it on the
    # deployed box rather than adding a near-duplicate file to the repo.
    STAGING_DEST="/etc/nginx/sites-available/$PUBLIC_DOMAIN.conf"
    if [ -f "$STAGING_DEST" ]; then
        skip "$PUBLIC_DOMAIN.conf already installed at $STAGING_DEST"
    else
        sed "s/anime\.earlcameron\.com/$PUBLIC_DOMAIN/g" \
            "$DEPLOY_DIR/nginx/anime.earlcameron.com.conf" > "$STAGING_DEST"
        chmod 0644 "$STAGING_DEST"
        done_ "generated $STAGING_DEST for $PUBLIC_DOMAIN from the production publish vhost"
    fi
    if [ ! -e "/etc/nginx/sites-enabled/$PUBLIC_DOMAIN.conf" ]; then
        ln -s "$STAGING_DEST" "/etc/nginx/sites-enabled/$PUBLIC_DOMAIN.conf"
        done_ "enabled $PUBLIC_DOMAIN.conf"
    else
        skip "$PUBLIC_DOMAIN.conf already enabled"
    fi
    warn "staging mode: admin vhost intentionally NOT installed — C2's milestone (PLAN.md"
    warn "§18) is publish-plane only. If this box ever needs an admin plane, run bootstrap"
    warn "again with AFF_DEPLOY_MODE=production against a real production hostname instead"
    warn "of bolting an admin vhost onto a staging box."
else
    # Publish vhost has no allowlist to guard — install unconditionally.
    install_vhost "$DEPLOY_DIR/nginx/anime.earlcameron.com.conf"

    step "admin nginx vhost — IP allowlist guard"
    ADMIN_SRC="$DEPLOY_DIR/nginx/admin.anime.earlcameron.com.conf"
    ADMIN_DEST="/etc/nginx/sites-available/admin.anime.earlcameron.com.conf"

    # The file as checked into git ALWAYS carries the placeholder — it must,
    # because a real home IP does not belong in a public repo. So the guard is
    # against the DEPLOYED copy in /etc/nginx, not the source: refuse to
    # (re)install or enable it there while the placeholder CIDR is present,
    # because leaving 0.0.0.0/0-equivalent reachability on the admin plane is
    # exactly the silent-public-admin mistake this design most invites.
    if [ -f "$ADMIN_DEST" ] && ! grep -q "$PLACEHOLDER_CIDR" "$ADMIN_DEST"; then
        skip "admin vhost already installed with a real allowlist CIDR"
        if [ ! -e "/etc/nginx/sites-enabled/admin.anime.earlcameron.com.conf" ]; then
            ln -s "$ADMIN_DEST" "/etc/nginx/sites-enabled/admin.anime.earlcameron.com.conf"
            done_ "enabled admin vhost"
        fi
    else
        if [ -n "$ADMIN_CIDR" ]; then
            case "$ADMIN_CIDR" in
                0.0.0.0/0|::/0)
                    echo "    REFUSING: $ADMIN_CIDR opens the admin plane to the whole internet." >&2
                    echo "    Pass the actual home IP as a /32 (or a narrow range), not a wildcard." >&2
                    exit 1
                    ;;
            esac
            sed "s#$PLACEHOLDER_CIDR#$ADMIN_CIDR#" "$ADMIN_SRC" > "$ADMIN_DEST"
            chmod 0644 "$ADMIN_DEST"
            done_ "installed admin vhost with allow $ADMIN_CIDR;"
            if [ ! -e "/etc/nginx/sites-enabled/admin.anime.earlcameron.com.conf" ]; then
                ln -s "$ADMIN_DEST" "/etc/nginx/sites-enabled/admin.anime.earlcameron.com.conf"
                done_ "enabled admin vhost"
            fi
        else
            warn "admin vhost NOT installed: no allowlist CIDR given and the placeholder"
            warn "($PLACEHOLDER_CIDR) is still in effect. Leaving it in place would make the"
            warn "admin plane reachable from anywhere the moment the vhost is enabled."
            warn "Re-run with the home IP:"
            warn "  sudo sh scripts/deploy-bootstrap.sh 203.0.113.7/32"
            DEFERRED="$DEFERRED admin-vhost"
        fi
    fi
fi

if command -v nginx >/dev/null 2>&1; then
    nginx -t
fi

# --- 5. certificates ---------------------------------------------------

step "TLS certificates (certbot)"
if command -v certbot >/dev/null 2>&1; then
    skip "certbot already installed"
else
    apt-get install -y certbot python3-certbot-nginx
    done_ "installed certbot"
fi

# Resolve a hostname before ever invoking certbot. certbot's own failure
# message when DNS simply is not live yet ("Timeout during connect" or a
# generic "Problem binding to port 80") reads like a firewall or nginx
# problem, not a DNS one — this turns that into an explicit, specific WARN
# instead of letting a confusing certbot error be the first signal.
dns_resolves() {
    domain="$1"
    if command -v getent >/dev/null 2>&1; then
        resolved=$(getent hosts "$domain" 2>/dev/null | awk '{print $1}' | head -n1)
    elif command -v host >/dev/null 2>&1; then
        resolved=$(host "$domain" 2>/dev/null | awk '/has address/ {print $4; exit}')
    else
        resolved=""
    fi
    [ -n "$resolved" ] || return 1
    done_ "$domain resolves to $resolved"
    return 0
}

issue_cert() {
    domain="$1"
    if [ -d "/etc/letsencrypt/live/$domain" ]; then
        skip "certificate for $domain already exists"
        return
    fi
    if ! dns_resolves "$domain"; then
        warn "$domain does not resolve yet — DNS must point at this droplet's public IP"
        warn "before a certificate can be issued. Not calling certbot (it would only fail"
        warn "with a confusing connect/timeout error). Add the A/AAAA record, then re-run"
        warn "this script."
        DEFERRED="$DEFERRED cert-$domain"
        return
    fi
    # --nginx both obtains and wires the cert into the already-installed
    # vhost; --non-interactive --agree-tos means this is safe to run
    # unattended on a re-run once DNS is actually live.
    if certbot --nginx -d "$domain" --non-interactive --agree-tos \
            --register-unsafely-without-email --redirect; then
        done_ "issued certificate for $domain"
    else
        warn "certbot failed for $domain even though it resolves — check certbot's own"
        warn "output above (rate limit, nginx syntax error, port 80 blocked upstream)."
        DEFERRED="$DEFERRED cert-$domain"
    fi
}

issue_cert "$PUBLIC_DOMAIN"
if [ "$DEPLOY_MODE" = "production" ]; then
    if [ -e "/etc/nginx/sites-enabled/admin.anime.earlcameron.com.conf" ]; then
        issue_cert "admin.anime.earlcameron.com"
    else
        warn "skipping admin.anime.earlcameron.com certificate — admin vhost is not enabled yet"
        DEFERRED="$DEFERRED cert-admin.anime.earlcameron.com"
    fi
fi

if command -v nginx >/dev/null 2>&1; then
    nginx -t && systemctl reload nginx
fi

# --- 6. summary --------------------------------------------------------

printf '\n==> bootstrap summary (mode: %s, domain: %s)\n' "$DEPLOY_MODE" "$PUBLIC_DOMAIN"
if [ -z "$DEFERRED" ]; then
    printf '    everything completed. Next: edit %s with real secrets if you have not,\n' "$ENV_FILE"
    printf '    then run scripts/deploy-release.sh with the image tag to deploy.\n'
    exit 0
else
    printf '    incomplete — the following need attention, then re-run this script:\n'
    for item in $DEFERRED; do
        printf '      - %s\n' "$item"
    done
    exit 1
fi
