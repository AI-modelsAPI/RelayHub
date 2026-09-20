CREATE TABLE channel_keys (
  id TEXT PRIMARY KEY NOT NULL,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  secret_ref TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  disabled INTEGER NOT NULL DEFAULT 0 CHECK(disabled IN (0,1)),
  created_at TEXT NOT NULL
);
CREATE INDEX channel_keys_channel_idx ON channel_keys(channel_id, disabled);
