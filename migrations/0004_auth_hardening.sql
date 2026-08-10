-- 0004 — auth hardening: password/pepper versioning and reset tokens
-- (PLAN.md §4, §10).
--
-- Two additions, both there because retrofitting them later is either hard or
-- impossible, not because they are needed on day one:

-- password_version travels with the hash so argon2id cost can be raised and
-- existing rows rehashed on next successful login, without a separate table
-- linking a hash back to the params it was created under (§4).
ALTER TABLE admin ADD COLUMN password_version INTEGER NOT NULL DEFAULT 1;

-- pepper_version exists BEFORE a pepper is configured, per §4: rotating a
-- pepper without a version column recorded from day one is not merely hard,
-- it is impossible, because there is nowhere to record which pepper a given
-- row was hashed under. 0 means "no pepper applied" — the default, and the
-- only value that can be true for every row already in this table.
ALTER TABLE admin ADD COLUMN pepper_version INTEGER NOT NULL DEFAULT 0;

-- Password reset tokens. Same opaque-token construction as sessions (§4):
-- 256 random bits, only the SHA-256 hash stored, so the table itself is not
-- a usable credential if read. single-use is enforced by the caller checking
-- and setting used_at, not by this schema — see internal/auth/reset.go.
CREATE TABLE password_reset_tokens (
    id         INTEGER PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    used_at    TEXT
) STRICT;

-- Reset flows are looked up by an incoming raw token (hashed then queried),
-- but expiry needs to be scanned/pruned without a table scan as the table
-- grows across the life of a single-admin install that never deletes rows.
CREATE INDEX password_reset_tokens_expires_at ON password_reset_tokens (expires_at);
