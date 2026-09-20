CREATE TABLE providers (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, adapter_type TEXT NOT NULL, protocol TEXT NOT NULL,
  base_url_template TEXT NOT NULL DEFAULT '', capabilities TEXT NOT NULL DEFAULT '',
  checkin_enabled INTEGER NOT NULL DEFAULT 0 CHECK(checkin_enabled IN (0,1)),
  adapter_version TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE channels (
  id TEXT PRIMARY KEY, provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  name TEXT NOT NULL, base_url TEXT NOT NULL, credential_ref TEXT NOT NULL DEFAULT '', account_ref TEXT NOT NULL DEFAULT '',
  routing_tags TEXT NOT NULL DEFAULT '', quota_state TEXT NOT NULL DEFAULT '', health_state TEXT NOT NULL DEFAULT '', rate_limit_state TEXT NOT NULL DEFAULT '',
  priority INTEGER NOT NULL DEFAULT 0, weight INTEGER NOT NULL DEFAULT 1 CHECK(weight > 0),
  checkin_enabled INTEGER NOT NULL DEFAULT 0 CHECK(checkin_enabled IN (0,1)), routing_enabled INTEGER NOT NULL DEFAULT 1 CHECK(routing_enabled IN (0,1)), enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX channels_provider_idx ON channels(provider_id);
CREATE INDEX channels_health_idx ON channels(enabled, routing_enabled, health_state, quota_state, priority);
CREATE TABLE credentials (
  id TEXT PRIMARY KEY, channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE, kind TEXT NOT NULL,
  ciphertext BLOB NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(channel_id, kind)
);
CREATE TABLE models (
  id TEXT PRIMARY KEY, display_name TEXT NOT NULL, aliases_json TEXT NOT NULL DEFAULT '[]', family TEXT NOT NULL DEFAULT '',
  protocol_requirements TEXT NOT NULL DEFAULT '', capabilities TEXT NOT NULL DEFAULT '', context_window INTEGER NOT NULL DEFAULT 0, max_output_tokens INTEGER NOT NULL DEFAULT 0,
  reasoning_support INTEGER NOT NULL DEFAULT 0 CHECK(reasoning_support IN (0,1)), vision_support INTEGER NOT NULL DEFAULT 0 CHECK(vision_support IN (0,1)), tool_call_support INTEGER NOT NULL DEFAULT 0 CHECK(tool_call_support IN (0,1)), enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE provider_models (
  id TEXT PRIMARY KEY, provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE, channel_id TEXT REFERENCES channels(id) ON DELETE CASCADE,
  model_id TEXT NOT NULL REFERENCES models(id) ON DELETE RESTRICT, upstream_model_name TEXT NOT NULL, protocol TEXT NOT NULL,
  request_transform TEXT NOT NULL DEFAULT '', response_transform TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL DEFAULT 0, weight INTEGER NOT NULL DEFAULT 1 CHECK(weight > 0), enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(provider_id, channel_id, model_id, upstream_model_name)
);
CREATE INDEX provider_models_lookup_idx ON provider_models(model_id, provider_id, channel_id, enabled);
CREATE TABLE model_groups (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', strategy TEXT NOT NULL, fallback_group_id TEXT REFERENCES model_groups(id) ON DELETE SET NULL,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)), created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE model_group_members (
  group_id TEXT NOT NULL REFERENCES model_groups(id) ON DELETE CASCADE, model_id TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  priority INTEGER NOT NULL DEFAULT 0, weight INTEGER NOT NULL DEFAULT 1 CHECK(weight > 0), PRIMARY KEY(group_id, model_id)
);
CREATE TABLE routes (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, protocol TEXT NOT NULL, model_pattern TEXT NOT NULL, group_id TEXT REFERENCES model_groups(id) ON DELETE SET NULL,
  channel_filter TEXT NOT NULL DEFAULT '', strategy TEXT NOT NULL, retry_policy TEXT NOT NULL DEFAULT '', fallback_policy TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)), created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX routes_match_idx ON routes(enabled, protocol, model_pattern, group_id);
CREATE TABLE checkin_records (
  id TEXT PRIMARY KEY, channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE, started_at TEXT NOT NULL, finished_at TEXT, status TEXT NOT NULL DEFAULT '', reward TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX checkin_channel_time_idx ON checkin_records(channel_id, started_at DESC);
CREATE TABLE health_records (
  id TEXT PRIMARY KEY, channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE, checked_at TEXT NOT NULL, status TEXT NOT NULL, latency_ms INTEGER NOT NULL DEFAULT 0, error_message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX health_channel_state_idx ON health_records(channel_id, status, checked_at DESC);
CREATE TABLE request_records (
  id TEXT PRIMARY KEY, request_id TEXT NOT NULL, protocol TEXT NOT NULL DEFAULT '', model_id TEXT, provider_id TEXT, channel_id TEXT,
  status_code INTEGER NOT NULL DEFAULT 0, latency_ms INTEGER NOT NULL DEFAULT 0, input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, error_class TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
  FOREIGN KEY(model_id) REFERENCES models(id) ON DELETE SET NULL, FOREIGN KEY(provider_id) REFERENCES providers(id) ON DELETE SET NULL, FOREIGN KEY(channel_id) REFERENCES channels(id) ON DELETE SET NULL
);
CREATE INDEX request_created_idx ON request_records(created_at DESC);
CREATE TABLE cli_sync_records (
  id TEXT PRIMARY KEY, cli TEXT NOT NULL, config_path TEXT NOT NULL, profile TEXT NOT NULL DEFAULT '', backup_path TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE config_backups (
  id TEXT PRIMARY KEY, path TEXT NOT NULL, sha256 TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE secret_values (
  ref TEXT PRIMARY KEY NOT NULL, ciphertext BLOB NOT NULL
);
CREATE TABLE local_api_keys (
  id TEXT PRIMARY KEY NOT NULL, key_hash BLOB NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at TEXT
);
CREATE TABLE audit_events (
  id TEXT PRIMARY KEY NOT NULL, action TEXT NOT NULL, request_id TEXT,
  task_id TEXT, actor TEXT, metadata_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
