-- 0003 — full-text search over items (PLAN.md §10, §12.4).
--
-- External-content FTS5: the index stores no copy of the text, only the terms,
-- and reads content back from `items` via `content_rowid`. That keeps one source
-- of truth. The price is that the index does not update itself — the triggers
-- below are not optional bookkeeping, they ARE the synchronisation, and a
-- missing one produces search results pointing at rows that no longer say what
-- the index thinks.

CREATE VIRTUAL TABLE items_fts USING fts5 (
    title,
    summary_text,
    body_html,
    content = 'items',
    content_rowid = 'id'
);

CREATE TRIGGER items_fts_insert AFTER INSERT ON items BEGIN
    INSERT INTO items_fts (rowid, title, summary_text, body_html)
    VALUES (new.id, new.title, new.summary_text, new.body_html);
END;

-- FTS5 external content requires the 'delete' command with the OLD values
-- before inserting the new ones; a plain UPDATE leaves the old terms indexed.
CREATE TRIGGER items_fts_delete AFTER DELETE ON items BEGIN
    INSERT INTO items_fts (items_fts, rowid, title, summary_text, body_html)
    VALUES ('delete', old.id, old.title, old.summary_text, old.body_html);
END;

CREATE TRIGGER items_fts_update AFTER UPDATE ON items BEGIN
    INSERT INTO items_fts (items_fts, rowid, title, summary_text, body_html)
    VALUES ('delete', old.id, old.title, old.summary_text, old.body_html);
    INSERT INTO items_fts (rowid, title, summary_text, body_html)
    VALUES (new.id, new.title, new.summary_text, new.body_html);
END;
