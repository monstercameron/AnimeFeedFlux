-- Sample (preview) spend, recorded where it survives the sample itself.
--
-- Previews already record what they cost, in samples.tokens_in/tokens_out/
-- cost_usd. Nothing could add it up, for a reason that is not an oversight:
-- samples are ephemeral by design. A row is deleted when it expires (24h
-- rolling TTL), when the operator discards it, and when it is promoted to a
-- real item. Summing the live samples table would therefore report a daily
-- total that SHRINKS through the day, and — worse — discarding a bad preview,
-- which is the normal thing to do with one, would erase its cost entirely.
-- Spend you can delete by clicking Discard is not a ceiling.
--
-- So the money is recorded separately from the artifact. A row here is written
-- in the same transaction as the sample it belongs to and is never deleted
-- with it; it outlives expiry, discard and promotion, because what a provider
-- call cost is a fact about the past and does not stop being true when the
-- thing it produced is thrown away.
--
-- Deliberately NOT a `runs` row. §11/§22 J3 is explicit that a sample must
-- never look like a publish, and reusing runs would put previews into run
-- history, run counts and the per-feed run cap. This table is the other half
-- of the total instead: internal/store sums runs and sample_spend together,
-- which is the combination flowtest's TestJ8_SamplingSpendInSameTotals proved
-- possible long before anything in production did it.
--
-- ON DELETE CASCADE matches runs: deleting a feed takes its spend history with
-- it, and the global total drops accordingly. That is the existing convention
-- for runs, not a separate judgement made here.
CREATE TABLE sample_spend (
    id         INTEGER PRIMARY KEY,
    feed_id    INTEGER NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    at         TEXT    NOT NULL,
    tokens_in  INTEGER NOT NULL DEFAULT 0,
    tokens_out INTEGER NOT NULL DEFAULT 0,
    cost_usd   REAL    NOT NULL DEFAULT 0
) STRICT;

-- Every read is "since <timestamp>", global or per feed: the daily and monthly
-- budget windows, and the per-day chart.
CREATE INDEX sample_spend_at ON sample_spend (at);
CREATE INDEX sample_spend_feed_at ON sample_spend (feed_id, at);
