-- 0001 — authentication and audit (PLAN.md §10, §4).
--
-- Single admin, no authorization model, so authentication is the entire
-- defence. These tables are the whole of it.

CREATE TABLE admin (
    id                  INTEGER PRIMARY KEY CHECK (id = 1), -- singleton
    password_hash       TEXT    NOT NULL,
    -- The argon2id parameters live beside the hash so they can be raised later
    -- and the row rehashed on next successful login. A hash whose cost is not
    -- recorded cannot be migrated without locking everyone out.
    kdf_params          TEXT    NOT NULL,
    totp_secret_enc     BLOB,
    created_at          TEXT    NOT NULL,
    password_changed_at TEXT
) STRICT;

CREATE TABLE recovery_codes (
    id        INTEGER PRIMARY KEY,
    code_hash TEXT NOT NULL UNIQUE,
    used_at   TEXT
) STRICT;

-- TOTP replay prevention. `step` is the PRIMARY KEY so the DATABASE rejects two
-- concurrent logins presenting the same code (§4). A check-then-insert in
-- application code is a TOCTOU race, and the whole point of a second factor is
-- that it is used exactly once.
CREATE TABLE totp_used (
    step      INTEGER PRIMARY KEY,
    code_hash TEXT NOT NULL,
    at        TEXT NOT NULL
) STRICT;

CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    ip           TEXT,
    user_agent   TEXT,
    revoked_at   TEXT
) STRICT;

CREATE INDEX sessions_expires_at ON sessions (expires_at);

CREATE TABLE auth_events (
    id     INTEGER PRIMARY KEY,
    at     TEXT    NOT NULL,
    kind   TEXT    NOT NULL,
    ip     TEXT,
    ok     INTEGER NOT NULL CHECK (ok IN (0, 1)),
    detail TEXT
) STRICT;

CREATE INDEX auth_events_at ON auth_events (at);
