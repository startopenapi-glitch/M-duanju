-- One immutable provider receipt per charge; legacy records never trigger a new debit.
CREATE TABLE IF NOT EXISTS generation_settlements (
 id VARCHAR(64) PRIMARY KEY DEFAULT app_gen_id(),
 provider VARCHAR(32) NOT NULL,
 kind VARCHAR(16) NOT NULL CHECK (kind IN ('video','image')),
 upstream_id VARCHAR(128) NOT NULL,
 project_id VARCHAR(64) NOT NULL REFERENCES projects(id),
 shot_id VARCHAR(64) REFERENCES shots(id),
 user_id VARCHAR(64) NOT NULL REFERENCES users(id),
 charge_micros BIGINT NOT NULL CHECK (charge_micros >= 0),
 credits BIGINT NOT NULL CHECK (credits >= 0),
 status VARCHAR(32) NOT NULL CHECK (status IN ('pending','settled','legacy_unverified')),
 result_url TEXT NOT NULL,
 result_duration DOUBLE PRECISION NOT NULL DEFAULT 0,
 result_width INTEGER NOT NULL DEFAULT 0,
 result_height INTEGER NOT NULL DEFAULT 0,
 credit_transaction_id VARCHAR(64) REFERENCES credit_transactions(id),
 pending_reason VARCHAR(32),
 pending_error TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 settled_at TIMESTAMPTZ,
 UNIQUE(provider,kind,upstream_id)
);
CREATE INDEX IF NOT EXISTS idx_generation_settlements_project ON generation_settlements(project_id,created_at);

-- An artifact belongs to the task that delivered it, even while a replacement runs.
ALTER TABLE shots ADD COLUMN IF NOT EXISTS delivered_upstream_id VARCHAR(128);
UPDATE shots SET delivered_upstream_id=upstream_id
WHERE COALESCE(upstream_id,'')<>''
  -- Only these completed states bind the current upstream id to the artifact.
  -- An old URL on QUEUED/PENDING/FAILED does not establish that relationship.
  AND status IN ('GENERATED','APPROVED')
  AND delivered_upstream_id IS NULL;
-- Only protect actual known task identities; these are not verified historic charges.
INSERT INTO generation_settlements(provider,kind,upstream_id,project_id,shot_id,user_id,charge_micros,credits,status,result_url,result_duration,result_width,result_height)
SELECT 'limeng','video',s.delivered_upstream_id,s.project_id,s.id,p.user_id,0,0,'legacy_unverified',COALESCE(s.video_url,''),COALESCE(s.duration_actual,0),COALESCE(s.width,0),COALESCE(s.height,0)
FROM shots s JOIN projects p ON p.id=s.project_id
WHERE COALESCE(s.delivered_upstream_id,'')<>''
ON CONFLICT(provider,kind,upstream_id) DO NOTHING;
-- keyvisual_task_id identifies the current image task and is cleared on delivery.
-- An old keyvisual_url plus any project UI status cannot prove that task delivered
-- it. Leave unknown image attribution unchanged; verified receipts retain their
-- existing identities and deduplication protection without an inferred backfill.
