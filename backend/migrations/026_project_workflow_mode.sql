-- Manual-first workflow. Existing projects are switched to manual; legacy
-- generation_runs remain for history/reconciliation only.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS workflow_mode VARCHAR(16) NOT NULL DEFAULT 'manual';
UPDATE projects SET workflow_mode='manual' WHERE workflow_mode IS DISTINCT FROM 'manual';
CREATE INDEX IF NOT EXISTS idx_projects_workflow_mode ON projects(workflow_mode);
