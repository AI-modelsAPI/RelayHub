ALTER TABLE request_records ADD COLUMN ttft_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_records ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_records ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_records ADD COLUMN finish_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE request_records ADD COLUMN upstream_model TEXT NOT NULL DEFAULT '';
