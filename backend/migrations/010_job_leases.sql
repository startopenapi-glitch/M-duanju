-- Submission intent is durable before any paid request. An unknown result retains
-- its reservation until an operator can bind a verified provider identity.
ALTER TABLE jobs ADD COLUMN lease_token BIGINT NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN phase VARCHAR(32) NOT NULL DEFAULT 'ready';
ALTER TABLE jobs ADD COLUMN reserved_credits BIGINT NOT NULL DEFAULT 0 CHECK (reserved_credits >= 0);
ALTER TABLE jobs ADD COLUMN error_code VARCHAR(48) NOT NULL DEFAULT '';
DROP INDEX idx_jobs_dedup;
CREATE UNIQUE INDEX idx_jobs_dedup ON jobs(kind,project_id,COALESCE(shot_id,''))
 WHERE status IN ('queued','running','reconcile');
-- Existing active jobs must query existing provider ids, never submit twice.
UPDATE jobs j SET upstream_id=s.upstream_id,phase='polling'
 FROM shots s WHERE j.kind='shot' AND j.shot_id=s.id AND j.status IN ('queued','running')
 AND COALESCE(s.upstream_id,'')<>'';
UPDATE jobs j SET upstream_id=p.keyvisual_task_id,phase='polling'
 FROM projects p WHERE j.kind='keyvisual' AND j.project_id=p.id AND j.status IN ('queued','running')
 AND COALESCE(p.keyvisual_task_id,'')<>'';
UPDATE jobs SET status='reconcile',phase='submitting',error_code='submission_unknown',
 last_error='旧任务提交结果未知，需要人工核对'
 WHERE status='running' AND kind IN ('shot','keyvisual') AND COALESCE(upstream_id,'')='';
UPDATE jobs SET status='queued',lease_token=lease_token+1 WHERE status='running';

-- Legacy active jobs predate reserved_credits: without a backfill their occupancy is 0,
-- so concurrent budget checks under-count and can over-commit. Estimate with the frozen
-- submitted resolution rate (unknown defaults to 720P), minimum 80; keyvisual 30.
UPDATE jobs j SET reserved_credits = GREATEST(80, FLOOR(s.duration * CASE j.payload->>'resolution' WHEN 'standard' THEN 20 WHEN '1080P' THEN 80 ELSE 40 END)::bigint)
 FROM shots s
 WHERE j.kind='shot' AND j.shot_id=s.id
   AND j.status IN ('queued','running','reconcile') AND j.reserved_credits=0;
UPDATE jobs SET reserved_credits = 30
 WHERE kind='keyvisual' AND status IN ('queued','running','reconcile') AND reserved_credits=0;

-- Reject ambiguous provider identity rather than silently merge ownership.
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_provider_identity ON jobs(kind,upstream_id)
 WHERE COALESCE(upstream_id,'')<>'';
