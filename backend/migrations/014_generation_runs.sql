-- 第一批：生成执行意图（run）+ 充值后续跑的持久化唤醒 outbox。
-- 不替换现有 jobs：一个 run 派生多个 job，复用既有租约/预占/结算。
-- 不改任何价格、积分换算与收费规则。

CREATE TABLE IF NOT EXISTS generation_runs (
  id               VARCHAR(32) PRIMARY KEY,
  user_id          VARCHAR(32) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  project_id       VARCHAR(32) NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
  project_version  INTEGER NOT NULL DEFAULT 1,
  mode             VARCHAR(16) NOT NULL DEFAULT 'first',
  status           VARCHAR(24) NOT NULL DEFAULT 'queued',
  idempotency_key  VARCHAR(64) NOT NULL,
  input_hash       VARCHAR(64) NOT NULL,
  scope            JSONB NOT NULL DEFAULT '{}'::jsonb,
  pricing_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
  checkpoint       VARCHAR(48) NOT NULL DEFAULT '',
  waiting_reason   VARCHAR(32) NOT NULL DEFAULT '',
  auto_resume      BOOLEAN NOT NULL DEFAULT TRUE,
  credits_estimated BIGINT NOT NULL DEFAULT 0,
  credits_settled   BIGINT NOT NULL DEFAULT 0,
  requested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  cancelled_at     TIMESTAMPTZ,
  completed_at     TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT generation_runs_mode_chk   CHECK (mode IN ('first','revise')),
  CONSTRAINT generation_runs_status_chk CHECK (status IN
    ('queued','running','waiting_credits','settlement_pending','completed','failed','cancelled'))
);
-- 同一项目同一幂等键只能有一个执行；不同输入用同键必须拒绝而不是覆盖
CREATE UNIQUE INDEX IF NOT EXISTS uniq_run_idem ON generation_runs(user_id, idempotency_key);
CREATE INDEX IF NOT EXISTS idx_run_project ON generation_runs(project_id, project_version DESC);
-- 同一项目只允许一个进行中的执行（服务端互斥，不依赖前端禁用按钮）
CREATE UNIQUE INDEX IF NOT EXISTS uniq_run_active ON generation_runs(project_id)
 WHERE status IN ('queued','running','waiting_credits','settlement_pending');

-- 充值到账后唤醒续跑：与入账同事务写入，崩溃后可恢复，支持重复投递
CREATE TABLE IF NOT EXISTS resume_wakeups (
  id         VARCHAR(32) PRIMARY KEY,
  run_id     VARCHAR(32) NOT NULL REFERENCES generation_runs(id) ON DELETE RESTRICT,
  order_id   VARCHAR(32) NOT NULL REFERENCES recharge_orders(id) ON DELETE RESTRICT,
  user_id    VARCHAR(32) NOT NULL,
  status     VARCHAR(16) NOT NULL DEFAULT 'pending',
  attempts   INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  run_after  TIMESTAMPTZ NOT NULL DEFAULT now(),
  claimed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT resume_wakeups_status_chk CHECK (status IN ('pending','claimed','done','dead'))
);
-- 同一 (run, order) 只唤醒一次；重复回调不会重复续跑
CREATE UNIQUE INDEX IF NOT EXISTS uniq_wakeup_run_order ON resume_wakeups(run_id, order_id);
CREATE INDEX IF NOT EXISTS idx_wakeup_due ON resume_wakeups(status, run_after);

-- 订单可携带可信续跑上下文；与原先的待支付订单复用保持兼容（不改写已有绑定）
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS run_id VARCHAR(32);
CREATE INDEX IF NOT EXISTS idx_order_run ON recharge_orders(run_id) WHERE run_id IS NOT NULL;
