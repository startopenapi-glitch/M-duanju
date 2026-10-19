package service

import (
	"database/sql"
	"errors"
	"strings"
)

func (s *Service) ListGenerationJobs(project string) ([]*Job, error) {
	rows, err := s.db.Query(`SELECT j.id,j.kind,j.project_id,COALESCE(j.shot_id,''),COALESCE(j.seq,0),j.status,j.phase,j.attempts,j.max_attempts,COALESCE(j.upstream_id,''),j.reserved_credits,j.error_code,COALESCE(j.last_error,''),j.run_after,
 COALESCE(g.credits,0),CASE WHEN g.id IS NULL THEN 0 ELSE GREATEST(0,LEAST(u.credits-r.user_reserved,p.budget_credits-p.spent_credits-r.project_reserved)) END
 FROM jobs j JOIN projects p ON p.id=j.project_id JOIN users u ON u.id=p.user_id
 LEFT JOIN generation_settlements g ON g.provider='limeng' AND g.kind=CASE WHEN j.kind='shot' THEN 'video' ELSE 'image' END AND g.upstream_id=j.upstream_id AND g.project_id=j.project_id AND COALESCE(g.shot_id,'')=COALESCE(j.shot_id,'') AND g.status='pending'
 CROSS JOIN LATERAL (SELECT COALESCE(sum(o.reserved_credits),0) user_reserved,COALESCE(sum(o.reserved_credits) FILTER(WHERE o.project_id=j.project_id),0) project_reserved FROM jobs o JOIN projects op ON op.id=o.project_id WHERE op.user_id=p.user_id AND o.id<>j.id) r
 WHERE j.project_id=$1 ORDER BY j.created_at DESC,j.id DESC LIMIT 100`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []*Job{}
	for rows.Next() {
		j := &Job{}
		if err = rows.Scan(&j.ID, &j.Kind, &j.ProjectID, &j.ShotID, &j.Seq, &j.Status, &j.Phase, &j.Attempts, &j.MaxAttempts, &j.UpstreamID, &j.ReservedCredits, &j.ErrorCode, &j.LastError, &j.RunAfter, &j.RequiredCredits, &j.AvailableCredits); err != nil {
			return nil, err
		}
		if j.RequiredCredits > j.AvailableCredits {
			j.ShortfallCredits = j.RequiredCredits - j.AvailableCredits
		}
		j.CanResume = j.Status == JobReconcile && j.UpstreamID != "" && j.Phase != "submitting"
		j.ManualRequired = j.ErrorCode == "submission_unknown"
		j.InfrastructureAllowance = 4
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// ResumeJob only resumes the original identified upstream task. It never clears
// identity, reservation or failure counters, and cannot unblock unknown creates.
func (s *Service) ResumeJob(project, id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, _, _, err = lockQueueProject(tx, project); err != nil {
		return err
	}
	var shot string
	if err = tx.QueryRow(`SELECT COALESCE(shot_id,'') FROM jobs WHERE id=$1 AND project_id=$2`, id, project).Scan(&shot); err != nil {
		return err
	}
	if shot != "" {
		var target string
		if err = tx.QueryRow(`SELECT id FROM shots WHERE id=$1 AND project_id=$2 FOR UPDATE`, shot, project).Scan(&target); err != nil {
			return err
		}
	}
	var status, phase, task, code string
	if err = tx.QueryRow(`SELECT status,phase,COALESCE(upstream_id,''),error_code FROM jobs WHERE id=$1 AND project_id=$2 FOR UPDATE`, id, project).Scan(&status, &phase, &task, &code); err != nil {
		return err
	}
	if task == "" || phase == "submitting" || code == "submission_unknown" {
		return ErrSubmissionUnknown
	}
	if status == JobDone || status == JobQueued || status == JobRunning {
		return nil
	}
	if status != JobReconcile {
		return errors.New("任务不处于待恢复状态")
	}
	if _, err = tx.Exec(`UPDATE jobs SET status='queued',run_after=now(),error_code='',updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) parkSettlement(j *Job, p *SettlementPendingError) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE jobs SET status='reconcile',error_code=$3,last_error=$4,updated_at=now()
		WHERE id=$1 AND lease_token=$2 AND status='running'`, j.ID, j.LeaseToken, "settlement_"+p.Reason, p.Error())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrLeaseLost
	}
	// R02：已核验结果待结算也算一种可恢复的等待，充值后应恢复原 job/receipt，
	// 而不是继续停在 running 且拒绝绑定充值。
	if j.RunID != "" {
		switch p.Reason {
		case "balance":
			if _, err = tx.Exec(`UPDATE generation_runs
				SET status='waiting_credits', waiting_reason='settlement',
				    required_credits=$2, updated_at=now()
				WHERE id=$1 AND status IN ('queued','running','waiting_credits')`, j.RunID, p.Required); err != nil {
				return err
			}
		case "ownership":
			// Transfer is not a recharge problem. Retain the original job for reconciliation.
			if _, err = tx.Exec(`UPDATE generation_runs SET status='failed',waiting_reason='project_changed',updated_at=now() WHERE id=$1 AND status IN ('queued','running','waiting_credits')`, j.RunID); err != nil {
				return err
			}
		case "budget":
			// 预算不足不能靠充值解决，必须与余额不足区分，交给明确原因/新授权。
			if _, err = tx.Exec(`UPDATE generation_runs
				SET status='failed', waiting_reason='budget_insufficient', updated_at=now()
				WHERE id=$1 AND status IN ('queued','running','waiting_credits')`, j.RunID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Used by operators for a verified provider ID only; deliberately not exposed to
// ordinary users, who cannot establish which paid submission an ID belongs to.
// Unknown submissions require provider-side evidence before this operation.
func (s *Service) ReconcileSubmission(project, id, verifiedTask string) error {
	return s.reconcileSubmission("", project, id, verifiedTask, "")
}
func (s *Service) AdminReconcileSubmission(actor, project, id, verifiedTask, reason string) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 1024 {
		return ErrAdminInvalid
	}
	return s.reconcileSubmission(actor, project, id, verifiedTask, reason)
}
func (s *Service) reconcileSubmission(actor, project, id, verifiedTask, reason string) error {
	if verifiedTask == "" {
		return ErrSubmissionUnknown
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if actor != "" {
		if err = activeAdminTx(tx, actor); err != nil {
			return err
		}
	}

	if _, _, _, _, err = lockQueueProject(tx, project); err != nil {
		return err
	}
	var shot, kind string
	if err = tx.QueryRow(`SELECT COALESCE(shot_id,''),kind FROM jobs WHERE id=$1 AND project_id=$2`, id, project).Scan(&shot, &kind); err != nil {
		return err
	}
	if shot != "" {
		var target string
		if err = tx.QueryRow(`SELECT id FROM shots WHERE id=$1 AND project_id=$2 FOR UPDATE`, shot, project).Scan(&target); err != nil {
			return err
		}
	}
	var status, phase, task string
	if err = tx.QueryRow(`SELECT status,phase,COALESCE(upstream_id,'') FROM jobs WHERE id=$1 AND project_id=$2 FOR UPDATE`, id, project).Scan(&status, &phase, &task); err != nil {
		return err
	}
	if status != JobReconcile || phase != "submitting" || task != "" {
		return errors.New("任务不处于提交未知状态")
	}
	if err = checkProviderBinding(tx, kind, project, shot, id, verifiedTask); err != nil {
		return err
	}
	if err = settlementExecOne(tx, "reconcile intent", `UPDATE jobs SET upstream_id=$1,phase='polling',status='queued',run_after=now(),error_code='',last_error=NULL,updated_at=now() WHERE id=$2`, verifiedTask, id); err != nil {
		return err
	}
	if shot != "" {
		err = settlementExecOne(tx, "reconcile video", `UPDATE shots SET upstream_id=$1,status='QUEUED' WHERE id=$2 AND project_id=$3`, verifiedTask, shot, project)
	} else {
		err = settlementExecOne(tx, "reconcile image", `UPDATE projects SET keyvisual_task_id=$1,status='KEYVISUAL_GENERATING' WHERE id=$2`, verifiedTask, project)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO audit_logs(project_id,shot_id,action,note,user_id) VALUES($1,$2,'reconcile_submission',$3,$4)`, project, nullIfEmpty(shot), "job="+id+" provider="+verifiedTask+" reason="+reason, nullIfEmpty(actor))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Called under the owning user/project/shot/job locks. All binding paths share
// this provider lock, including identities only present in pre-queue records.
func checkProviderBinding(tx *sql.Tx, kind, project, shot, job, task string) error {
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "provider:"+kind+":"+task); err != nil {
		return err
	}
	receiptKind := "image"
	if kind == JobShot {
		receiptKind = "video"
	}
	var used bool
	err := tx.QueryRow(`SELECT EXISTS(
 SELECT 1 FROM jobs WHERE kind=$1 AND upstream_id=$2 AND id<>$3
 UNION ALL SELECT 1 FROM generation_settlements WHERE kind=$4 AND upstream_id=$2 AND (project_id<>$5 OR COALESCE(shot_id,'')<>$6)
 UNION ALL SELECT 1 FROM shots WHERE $1='shot' AND (upstream_id=$2 OR delivered_upstream_id=$2) AND (project_id<>$5 OR id<>$6)
 UNION ALL SELECT 1 FROM projects WHERE $1='keyvisual' AND keyvisual_task_id=$2 AND id<>$5
 )`, kind, task, job, receiptKind, project, shot).Scan(&used)
	if err != nil {
		return err
	}
	if used {
		return errors.New("provider identity already bound")
	}
	return nil
}

// A worker may crash after settlement and before marking its job done. Resume
// from the trusted local receipt, without fetching an expired upstream result.
func (s *Service) finishDeliveredJob(j *Job) (bool, error) {
	if j.UpstreamID == "" {
		return false, nil
	}
	done := false
	err := s.jobMutation(j, func(tx *sql.Tx) error {
		kind := "image"
		if j.Kind == JobShot {
			kind = "video"
		}
		var status, project, shot, owner string
		err := tx.QueryRow(`SELECT status,project_id,COALESCE(shot_id,''),user_id FROM generation_settlements WHERE provider='limeng' AND kind=$1 AND upstream_id=$2 FOR UPDATE`, kind, j.UpstreamID).Scan(&status, &project, &shot, &owner)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var currentOwner string
		if err = tx.QueryRow(`SELECT user_id FROM projects WHERE id=$1`, j.ProjectID).Scan(&currentOwner); err != nil {
			return err
		}
		if project != j.ProjectID || shot != j.ShotID || owner != currentOwner {
			return errors.New("conflicting settlement owner")
		}
		if status != "settled" && status != "legacy_unverified" {
			return nil
		}
		if err = settlementExecOne(tx, "finish delivered job", `UPDATE jobs SET status='done',phase='done',reserved_credits=0,error_code='',last_error=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running'`, j.ID, j.LeaseToken); err != nil {
			return err
		}
		done = true
		return nil
	})
	return done, err
}

func (s *Service) ApproveKeyVisual(project string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, _, _, err = lockQueueProject(tx, project); err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM jobs WHERE project_id=$1 AND kind='keyvisual' AND status IN ('queued','running','reconcile'))`, project).Scan(&active); err != nil {
		return err
	}
	if active {
		return errors.New("定妆照正在生成或待恢复，请等待当前任务完成")
	}
	if err = settlementExecOne(tx, "approve keyvisual", `UPDATE projects SET status='KEYVISUAL_REVIEW',updated_at=now() WHERE id=$1 AND status='KEYVISUAL_REVIEW' AND COALESCE(keyvisual_url,'')<>''`, project); err != nil {
		return errors.New("定妆照尚未就绪，请刷新后重试")
	}
	return tx.Commit()
}
