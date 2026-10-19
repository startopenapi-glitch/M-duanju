-- 让定妆照任务可跨重启恢复
ALTER TABLE projects ADD COLUMN IF NOT EXISTS keyvisual_task_id VARCHAR(64);
ALTER TABLE projects ADD COLUMN IF NOT EXISTS keyvisual_started_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_projects_keyvisual_task ON projects(keyvisual_task_id) WHERE keyvisual_task_id IS NOT NULL;
