-- 009: persist per-channel custom identity headers (AUDIT RH-09) and speed up
-- the per-channel recent-request lookups used by usage stats (AUDIT RH-32).

ALTER TABLE channels ADD COLUMN custom_headers TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS request_channel_created_idx ON request_records(channel_id, created_at DESC);
