-- 将 job 关联到发起它的生成执行（run），便于按执行追踪产物、预占与结算。
-- 历史 job / 旧手动入口允许为 NULL；不为历史数据回填，避免把未授权任务算进执行。
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS run_id VARCHAR(32)
  REFERENCES generation_runs(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_jobs_run ON jobs(run_id) WHERE run_id IS NOT NULL;
