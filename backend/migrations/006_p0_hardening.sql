-- P0：密码管理 + TOTP 双因素
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret        VARCHAR(64);
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled       BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_pending_secret VARCHAR(64);  -- 待确认的 secret
ALTER TABLE users ADD COLUMN IF NOT EXISTS recovery_codes     TEXT;          -- 恢复码，JSON 数组（已哈希）
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at      TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS failed_login_count INT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_until       TIMESTAMPTZ;

-- P0：任务表（worker 扫表用，替代 API 进程内 goroutine）
CREATE TABLE IF NOT EXISTS jobs (
  id           VARCHAR(64) PRIMARY KEY DEFAULT app_gen_id(),
  kind         VARCHAR(32) NOT NULL,        -- keyvisual / shot / assemble
  project_id   VARCHAR(64) NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  shot_id      VARCHAR(64),
  seq          INT,
  status       VARCHAR(24) NOT NULL DEFAULT 'queued',  -- queued/running/done/failed
  attempts     INT NOT NULL DEFAULT 0,
  max_attempts INT NOT NULL DEFAULT 2,
  upstream_id  VARCHAR(64),
  payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
  last_error   TEXT,
  heartbeat_at TIMESTAMPTZ,
  run_after    TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_jobs_claim ON jobs(kind, status, run_after)
  WHERE status IN ('queued','running');
CREATE INDEX IF NOT EXISTS idx_jobs_project ON jobs(project_id, created_at DESC);
-- 同一段不重复排队
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_dedup
  ON jobs(kind, project_id, COALESCE(shot_id,''))
  WHERE status IN ('queued','running');

-- P1：退款记录
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS refund_amount_cents BIGINT NOT NULL DEFAULT 0;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS refund_reason      TEXT;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS refunded_at        TIMESTAMPTZ;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS refund_no          VARCHAR(64);
CREATE INDEX IF NOT EXISTS idx_orders_refund ON recharge_orders(refunded_at)
  WHERE refunded_at IS NOT NULL;
