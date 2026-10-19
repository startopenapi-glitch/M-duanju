-- User-facing project deletion is a soft delete: history and direct URLs stop
-- resolving, while settlements, credit transactions and audit records remain.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS deleted_by VARCHAR(64) REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_projects_active_user
  ON projects(user_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_deleted_at
  ON projects(deleted_at) WHERE deleted_at IS NOT NULL;
