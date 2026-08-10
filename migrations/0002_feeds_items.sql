-- 0002 — settings, feeds, and items (PLAN.md §10).

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

CREATE TABLE feeds (
    id                   INTEGER PRIMARY KEY,
    slug                 TEXT    NOT NULL UNIQUE,
    title                TEXT    NOT NULL,
    description          TEXT    NOT NULL DEFAULT '',
    language             TEXT    NOT NULL DEFAULT 'en',
    kind                 TEXT    NOT NULL CHECK (kind IN ('generative', 'grounded', 'aggregate')),
    spec_json            TEXT    NOT NULL DEFAULT '{}',
    enabled              INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    timezone             TEXT    NOT NULL DEFAULT 'UTC',
    -- Derived from hash(slug) and stored so the UI's "next three runs" readback
    -- matches what the scheduler will actually do (§14.3). Recomputing it at
    -- display time would be a second implementation that can disagree.
    jitter_offset        INTEGER NOT NULL DEFAULT 0,
    last_fired_slot      TEXT,
    last_built_at        TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    author               TEXT,
    copyright            TEXT,
    og_image             TEXT,
    ttl_minutes          INTEGER NOT NULL DEFAULT 15,
    version              INTEGER NOT NULL DEFAULT 1, -- optimistic concurrency (§11)
    deleted_at           TEXT,
    created_at           TEXT    NOT NULL,
    updated_at           TEXT    NOT NULL
) STRICT;

CREATE TABLE feed_members (
    aggregate_feed_id INTEGER NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    member_feed_id    INTEGER NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    position          INTEGER NOT NULL,
    PRIMARY KEY (aggregate_feed_id, member_feed_id)
) STRICT;

CREATE TABLE runs (
    id                  INTEGER PRIMARY KEY,
    feed_id             INTEGER NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    started_at          TEXT    NOT NULL,
    finished_at         TEXT,
    status              TEXT    NOT NULL CHECK (status IN
                            ('running', 'success', 'failed', 'skipped', 'interrupted', 'completed_unconfirmed')),
    error_kind          TEXT,
    error               TEXT,
    items_added         INTEGER NOT NULL DEFAULT 0,
    items_rejected      INTEGER NOT NULL DEFAULT 0,
    reject_reasons_json TEXT    NOT NULL DEFAULT '{}',
    tokens_in           INTEGER NOT NULL DEFAULT 0,
    tokens_out          INTEGER NOT NULL DEFAULT 0,
    est_cost_usd        REAL    NOT NULL DEFAULT 0,
    -- Only two producers exist: the scheduler and RunNow. Sampling writes a
    -- samples row, never a run, and there is no backfill entry point (§10).
    trigger             TEXT    NOT NULL CHECK (trigger IN ('cron', 'manual')),
    lock_holder         TEXT,
    heartbeat_at        TEXT
) STRICT;

CREATE INDEX runs_feed_started ON runs (feed_id, started_at DESC);

CREATE TABLE items (
    id            INTEGER PRIMARY KEY,
    feed_id       INTEGER NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    -- Opaque ULID, assigned once, NEVER derived from content (§5.1). The guid
    -- and the Atom id are built from it, and a guid must be stable across an
    -- edit or every subscriber sees the item again as a duplicate.
    item_key      TEXT    NOT NULL UNIQUE,
    -- Separate from identity, and only for idempotency: a repeated run must not
    -- add the same trivia question twice.
    content_hash  TEXT    NOT NULL,
    title         TEXT    NOT NULL,
    -- PLAIN TEXT. Becomes <description> and og:description. Slack renders a
    -- snippet and mangles rich HTML, so the markup lives in body_html (§5.5).
    summary_text  TEXT    NOT NULL,
    body_html     TEXT    NOT NULL,
    -- Trivia only. Must never reach summary_text or og:description, or the
    -- channel preview spoils the question (§5.5).
    answer_html   TEXT,
    link          TEXT,
    source_name   TEXT,
    published_at  TEXT    NOT NULL,
    model         TEXT,
    prompt_hash   TEXT,
    tokens_in     INTEGER NOT NULL DEFAULT 0,
    tokens_out    INTEGER NOT NULL DEFAULT 0,
    run_id        INTEGER REFERENCES runs (id) ON DELETE SET NULL,
    origin        TEXT    NOT NULL CHECK (origin IN ('generated', 'sampled', 'manual', 'correction')),
    version       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL,
    edited_at     TEXT,
    -- SOFT delete only. The guid is never freed and the permalink 410s forever;
    -- there is no hard delete (§12.4).
    deleted_at    TEXT,

    UNIQUE (feed_id, content_hash),
    -- Slack drops items that share a timestamp, silently (§5.5). A constraint,
    -- not a convention.
    UNIQUE (feed_id, published_at)
) STRICT;

CREATE INDEX items_feed_published ON items (feed_id, published_at DESC);
CREATE INDEX items_deleted ON items (deleted_at);

CREATE TABLE item_revisions (
    id        INTEGER PRIMARY KEY,
    item_id   INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    at        TEXT    NOT NULL,
    field     TEXT    NOT NULL,
    old_value TEXT,
    new_value TEXT
) STRICT;

CREATE TABLE corrections (
    item_id          INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    corrects_item_id INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    PRIMARY KEY (item_id, corrects_item_id)
) STRICT;

CREATE TABLE item_embeddings (
    item_id INTEGER PRIMARY KEY REFERENCES items (id) ON DELETE CASCADE,
    model   TEXT    NOT NULL,
    dim     INTEGER NOT NULL,
    vec     BLOB    NOT NULL
) STRICT;

CREATE TABLE samples (
    id           INTEGER PRIMARY KEY,
    feed_id      INTEGER NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    created_at   TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL,
    payload_json TEXT    NOT NULL,
    tokens_in    INTEGER NOT NULL DEFAULT 0,
    tokens_out   INTEGER NOT NULL DEFAULT 0,
    cost_usd     REAL    NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX samples_expires ON samples (expires_at);

CREATE TABLE sources (
    id              INTEGER PRIMARY KEY,
    feed_id         INTEGER NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    url             TEXT    NOT NULL,
    kind            TEXT    NOT NULL DEFAULT 'rss',
    last_fetched_at TEXT,
    last_etag       TEXT,
    last_modified   TEXT,
    last_status     INTEGER,
    UNIQUE (feed_id, url)
) STRICT;
