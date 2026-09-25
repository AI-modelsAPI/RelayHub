-- 010: billing reconciliation history (AUDIT §5 B2). One row per channel per
-- pass: local token ledger versus the site's own consumption log, plus the
-- effective price the pass derived. Metadata only — no prompts, responses or
-- credentials.

CREATE TABLE IF NOT EXISTS billing_reports (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL,
  window_start TEXT NOT NULL,
  window_end TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  requests INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens INTEGER NOT NULL DEFAULT 0,
  tokens INTEGER NOT NULL DEFAULT 0,
  charged_tokens INTEGER NOT NULL DEFAULT 0,
  charged_quota INTEGER NOT NULL DEFAULT 0,
  charged_usd REAL NOT NULL DEFAULT 0,
  declared_usd REAL NOT NULL DEFAULT 0,
  declared_known INTEGER NOT NULL DEFAULT 0,
  effective_usd_per_mtok REAL NOT NULL DEFAULT 0,
  declared_usd_per_mtok REAL NOT NULL DEFAULT 0,
  baseline_usd_per_mtok REAL NOT NULL DEFAULT 0,
  drift REAL NOT NULL DEFAULT 0,
  token_drift REAL NOT NULL DEFAULT 0,
  multiplier_drift REAL NOT NULL DEFAULT 0,
  savings_usd REAL NOT NULL DEFAULT 0,
  group_ratio REAL NOT NULL DEFAULT 0,
  model_ratio REAL NOT NULL DEFAULT 0,
  entries INTEGER NOT NULL DEFAULT 0,
  matched_models INTEGER NOT NULL DEFAULT 0,
  truncated INTEGER NOT NULL DEFAULT 0,
  verdict TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS billing_reports_channel_created_idx
  ON billing_reports(channel_id, created_at DESC);
