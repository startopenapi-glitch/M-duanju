-- Administrative actions are idempotent and auditable. No financial policy changes.
CREATE TABLE IF NOT EXISTS admin_action_requests (
 actor_id VARCHAR(64) NOT NULL,
 request_id VARCHAR(36) NOT NULL,
 payload_hash VARCHAR(64) NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_id,request_id)
);
CREATE TABLE IF NOT EXISTS worker_heartbeats (
 name VARCHAR(64) PRIMARY KEY,
 last_seen TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_admin_orders_created ON recharge_orders(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_admin_jobs_updated ON jobs(updated_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_admin_audit_created ON audit_logs(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_admin_credits_created ON credit_transactions(created_at DESC,id DESC);

-- Typed administrative subject; do not infer a target from free-form notes.
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS target_id VARCHAR(64);
CREATE INDEX IF NOT EXISTS idx_admin_audit_target ON audit_logs(target_id,created_at DESC);
