package service

import (
	"database/sql"
	"errors"
)

func scriptEditable(tx *sql.Tx, project string) error {
	var protected bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND (spent_credits>0 OR COALESCE(keyvisual_url,'')<>'' OR COALESCE(final_url,'')<>'' OR COALESCE(keyvisual_task_id,'')<>''))
 OR EXISTS(SELECT 1 FROM jobs WHERE project_id=$1)
 OR EXISTS(SELECT 1 FROM generation_settlements WHERE project_id=$1)
 OR EXISTS(SELECT 1 FROM shots WHERE project_id=$1 AND (status<>'PENDING' OR retry_count>0 OR COALESCE(upstream_id,'')<>'' OR COALESCE(delivered_upstream_id,'')<>'' OR COALESCE(video_url,'')<>'' OR COALESCE(charge_micros,0)>0))`, project).Scan(&protected)
	if err != nil {
		return err
	}
	if protected {
		return errors.New("已有生成任务、费用或素材，不能通过重试剧本覆盖；请新建项目")
	}
	return nil
}
func (s *Service) claimScript(project string) (int64, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	if _, _, _, _, e = lockQueueProject(tx, project); e != nil {
		return 0, e
	}
	var status string
	var live bool
	if e = tx.QueryRow(`SELECT status,COALESCE(script_lease_until>now(),false) FROM projects WHERE id=$1`, project).Scan(&status, &live); e != nil {
		return 0, e
	}
	if status != "DRAFT" && status != "SCRIPT_REVIEW" && !(status == "SCRIPT_GENERATING" && !live) {
		return 0, errors.New("当前阶段不能重试剧本，请等待已有任务完成")
	}
	if e = scriptEditable(tx, project); e != nil {
		return 0, e
	}
	var token int64
	e = tx.QueryRow(`UPDATE projects SET status='SCRIPT_GENERATING',script_generation_token=script_generation_token+1,script_lease_until=now()+interval '4 minutes',error_msg=NULL,updated_at=now() WHERE id=$1 RETURNING script_generation_token`, project).Scan(&token)
	if e != nil {
		return 0, e
	}
	return token, tx.Commit()
}
func (s *Service) releaseScript(project string, token int64) {
	// CAS: failure from an old attempt can never reset its successor.
	s.db.Exec(`UPDATE projects SET status=CASE WHEN EXISTS(SELECT 1 FROM shots WHERE project_id=$1) THEN 'SCRIPT_REVIEW' ELSE 'DRAFT' END,script_lease_until=NULL,error_msg='剧本生成未完成，可在当前项目重试',updated_at=now() WHERE id=$1 AND script_generation_token=$2 AND status='SCRIPT_GENERATING'`, project, token)
}

// Serialize editors with script claim/save. Budget-only changes do not alter script facts.
func (s *Service) beginProjectEdit(project string, content bool) (*sql.Tx, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	if _, _, _, _, e = lockQueueProject(tx, project); e != nil {
		tx.Rollback()
		return nil, e
	}
	if content {
		var state string
		if e = tx.QueryRow(`SELECT status FROM projects WHERE id=$1`, project).Scan(&state); e != nil {
			tx.Rollback()
			return nil, e
		}
		if state == "SCRIPT_GENERATING" {
			tx.Rollback()
			return nil, errors.New("剧本正在生成，内容暂不可编辑；预算仍可单独调整")
		}
	}
	return tx, nil
}
