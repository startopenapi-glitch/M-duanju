CREATE TABLE IF NOT EXISTS generation_revision_plans (
 run_id VARCHAR(64) PRIMARY KEY REFERENCES generation_runs(id),
 base_snapshot JSONB NOT NULL,
 changes JSONB NOT NULL,
 applied_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
