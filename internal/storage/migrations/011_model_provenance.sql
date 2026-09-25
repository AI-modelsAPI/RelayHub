-- 011: model provenance (AUDIT 2026-09-24 §5 B3).
--
-- channels.official_source marks a channel the operator vouches for as the
-- vendor's own endpoint. Only such a channel may claim reserved vendor model
-- names (claude-*, gpt-*, ...) automatically during a model sync.
--
-- provider_models.held_reason records why a synced binding is waiting for
-- review (reserved_model_name / served_elsewhere). An empty value means the
-- binding is live or was curated by hand.
--
-- model_sync_snapshots keeps the before-image of each sync so one click can
-- undo it. Metadata only: binding configuration and the IDs of global models
-- the sync created.
--
-- NOTE: the migration runner splits this file on semicolons, so comments here
-- must not contain any.

ALTER TABLE channels ADD COLUMN official_source INTEGER NOT NULL DEFAULT 0;

ALTER TABLE provider_models ADD COLUMN held_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS model_sync_snapshots (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  bindings TEXT NOT NULL DEFAULT '[]',
  added_models TEXT NOT NULL DEFAULT '[]',
  held TEXT NOT NULL DEFAULT '[]',
  undone INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS model_sync_snapshots_channel_idx
  ON model_sync_snapshots(channel_id, created_at DESC);
