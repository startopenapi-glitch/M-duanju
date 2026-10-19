-- R04：把「本次授权范围估算」「下一阶段总需求」「缺口」拆成不同事实。
-- required_credits 只表示恢复本执行还需要的总积分；缺口由展示/恢复时用可用积分实时计算。
ALTER TABLE generation_runs ADD COLUMN IF NOT EXISTS required_credits BIGINT NOT NULL DEFAULT 0;

-- R05：完成必须指向本次执行实际发布的成片，不能拿旧 final_url 冒充新版本交付。
ALTER TABLE projects ADD COLUMN IF NOT EXISTS final_run_id VARCHAR(32)
  REFERENCES generation_runs(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_jobs_run_status ON jobs(run_id, status) WHERE run_id IS NOT NULL;
