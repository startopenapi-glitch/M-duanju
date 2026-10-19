-- Immutable outputs keep historical versions readable without publishing over a successor.
CREATE TABLE IF NOT EXISTS generation_artifacts (
 id BIGSERIAL PRIMARY KEY,
 run_id VARCHAR(64) NOT NULL REFERENCES generation_runs(id),
 job_id VARCHAR(64) NOT NULL REFERENCES jobs(id),
 kind VARCHAR(16) NOT NULL CHECK(kind IN ('image','video','final')),
 target_id VARCHAR(64) NOT NULL DEFAULT '',
 upstream_id VARCHAR(128) NOT NULL DEFAULT '',
 result_url TEXT NOT NULL,
 metadata JSONB NOT NULL DEFAULT '{}',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(job_id,kind)
);
CREATE INDEX IF NOT EXISTS idx_generation_artifacts_run ON generation_artifacts(run_id,id);
CREATE TABLE IF NOT EXISTS generation_artifact_reuses (
 run_id VARCHAR(64) NOT NULL REFERENCES generation_runs(id),
 artifact_id BIGINT NOT NULL REFERENCES generation_artifacts(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(run_id,artifact_id)
);
