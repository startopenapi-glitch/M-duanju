-- Preserve every acknowledged request key, including active-run reuse. A lost
-- response must still resolve to the same execution after it becomes terminal.
-- Only original keys are recoverable from historical data; do not invent aliases.
CREATE TABLE IF NOT EXISTS generation_run_requests (
  user_id VARCHAR(32) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  request_key VARCHAR(64) NOT NULL,
  run_id VARCHAR(32) NOT NULL REFERENCES generation_runs(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, request_key)
);
CREATE INDEX IF NOT EXISTS idx_run_request_run ON generation_run_requests(run_id);

INSERT INTO generation_run_requests(user_id,request_key,run_id)
SELECT user_id,idempotency_key,id FROM generation_runs
ON CONFLICT (user_id,request_key) DO NOTHING;
