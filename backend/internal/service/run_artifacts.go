package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Called under existing project/job locks. Recording an old job's result is
// separate from publishing the current project's view; financial facts survive.
func (s *Service) recordRunArtifactTx(tx *sql.Tx, kind, url, task string, metadata any) (bool, error) {
	if s.job == nil || s.job.RunID == "" {
		return true, nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return false, err
	}
	var stored string
	err = tx.QueryRow(`INSERT INTO generation_artifacts(run_id,job_id,kind,target_id,upstream_id,result_url,metadata)
 VALUES($1,$2,$3,$4,$5,$6,$7::jsonb) ON CONFLICT(job_id,kind) DO UPDATE SET job_id=EXCLUDED.job_id
 RETURNING result_url`, s.job.RunID, s.job.ID, kind, s.job.ShotID, task, url, string(raw)).Scan(&stored)
	if err != nil {
		return false, err
	}
	if stored != url {
		return false, errors.New("conflicting immutable run artifact")
	}
	var current bool
	err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM generation_runs r JOIN projects p ON p.id=r.project_id AND p.user_id=r.user_id
 WHERE r.id=$1 AND r.project_id=$2 AND NOT EXISTS(SELECT 1 FROM generation_runs n WHERE n.project_id=r.project_id AND n.project_version>r.project_version))`, s.job.RunID, s.job.ProjectID).Scan(&current)
	return current, err
}

// Explicitly reuse a completed old image/shot on a continuation. No new task or
// debit is created, and only the still-matching upstream identity can be adopted.
func (s *Service) adoptRunArtifacts(ctx context.Context, runID, project string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = authorizeRunTx(tx, project, runID); err != nil {
		return err
	}
	if _, _, _, _, err = lockQueueProject(tx, project); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT a.id,a.kind,a.target_id,a.upstream_id,a.result_url,a.metadata::text
 FROM generation_artifacts a JOIN jobs j ON j.id=a.job_id JOIN generation_runs r ON r.id=a.run_id
 WHERE j.project_id=$1 AND j.status='done' AND a.run_id<>$2 AND a.kind IN ('image','video')
 AND r.user_id=(SELECT user_id FROM generation_runs WHERE id=$2) ORDER BY a.id`, project, runID)
	if err != nil {
		return err
	}
	type artifact struct {
		id                                int64
		kind, target, task, url, metadata string
	}
	list := []artifact{}
	for rows.Next() {
		var a artifact
		if err = rows.Scan(&a.id, &a.kind, &a.target, &a.task, &a.url, &a.metadata); err != nil {
			rows.Close()
			return err
		}
		list = append(list, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range list {
		var res sql.Result
		if a.kind == "image" {
			res, err = tx.Exec(`UPDATE projects SET keyvisual_url=$1,keyvisual_task_id=NULL,status='KEYVISUAL_REVIEW',updated_at=now()
 WHERE id=$2 AND status='KEYVISUAL_GENERATING' AND keyvisual_task_id=$3`, a.url, project, a.task)
		} else {
			var facts GenerationSettlement
			if err = json.Unmarshal([]byte(a.metadata), &facts); err != nil {
				return err
			}
			if facts.Kind != "video" || facts.TaskID != a.task || facts.ProjectID != project || facts.ShotID != a.target || facts.URL != a.url {
				return errors.New("artifact metadata mismatch")
			}
			res, err = tx.Exec(`UPDATE shots SET video_url=$1,delivered_upstream_id=$3,status='GENERATED',duration_actual=$5,width=$6,height=$7,charge_micros=$8,error_msg=NULL,updated_at=now()
 WHERE id=$2 AND project_id=$4 AND upstream_id=$3 AND status='QUEUED'`, a.url, a.target, a.task, project, facts.Duration, facts.Width, facts.Height, facts.ChargeMicros)
		}
		if err != nil {
			return err
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n > 0 {
			if _, err = tx.Exec(`INSERT INTO generation_artifact_reuses(run_id,artifact_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, runID, a.id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// History reads only outputs owned by the current project/account. A legacy
// snapshot without a known run remains labelled historical, never assigned one.
type RunOutput struct {
	RunID          string `json:"runId"`
	ProjectVersion int    `json:"projectVersion"`
	URL            string `json:"url"`
}

func (s *Service) deliveredRunOutputs(user, project string) ([]RunOutput, error) {
	rows, err := s.db.Query(`SELECT DISTINCT run_id,output_version,url FROM (
 SELECT r.id AS run_id,r.project_version AS output_version,a.result_url AS url FROM generation_artifacts a JOIN generation_runs r ON r.id=a.run_id
 WHERE r.project_id=$1 AND r.user_id=$2 AND a.kind='final'
 UNION ALL
 SELECT COALESCE(old.id,''),COALESCE(old.project_version,0),p.base_snapshot->'project'->>'final_url'
 FROM generation_revision_plans p JOIN generation_runs current ON current.id=p.run_id
 LEFT JOIN generation_runs old ON old.id=p.base_snapshot->'project'->>'final_run_id' AND old.project_id=$1 AND old.user_id=$2
 WHERE current.project_id=$1 AND current.user_id=$2 AND p.base_snapshot->'project'->>'user_id'=$2
 ) outputs WHERE COALESCE(url,'')<>'' ORDER BY output_version DESC,run_id,url LIMIT 100`, project, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunOutput{}
	for rows.Next() {
		var o RunOutput
		if err = rows.Scan(&o.RunID, &o.ProjectVersion, &o.URL); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
