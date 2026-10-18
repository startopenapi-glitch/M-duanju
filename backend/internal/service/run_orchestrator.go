package service

// 生成执行的编排：由 worker 周期推进，不依赖浏览器保持打开。
//
// 只推进有 generation_runs 记录的项目——历史手工项目不会因为升级后被批量自动消费。
// 单个 run 的推进用 PostgreSQL advisory lock 串行化，避免多 worker 重复派发；
// 具体 job 的幂等仍由既有 jobs 约束与 EnqueueJob/queueShots 保证。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	runpkg "drama-platform/internal/run"
)

const (
	runStageLease = 15 * time.Minute
	runStageTries = 6
)

// AdvanceRun 推进一次执行：按项目当前阶段派发下一步，直到进入等待或完成。
func (s *Service) AdvanceRun(ctx context.Context, runID string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	key := "run-advance:" + runID
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, key).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil // 已有 worker 在处理
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, key)
	return s.advanceRunLocked(ctx, runID)
}

func (s *Service) advanceRunLocked(ctx context.Context, runID string) error {
	var userID, projectID, mode string
	var autoResume bool
	err := s.db.QueryRowContext(ctx, `SELECT user_id,project_id,auto_resume,mode
		FROM generation_runs WHERE id=$1 AND status IN ('queued','running')`, runID).
		Scan(&userID, &projectID, &autoResume, &mode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !autoResume {
		return s.finishRun(ctx, runID, "cancelled", "auto_resume_off")
	}
	if mode == "revise" {
		if err := s.applyRevision(ctx, runID, projectID); err != nil {
			if errors.Is(err, ErrRunNotAuthorized) {
				return nil
			}
			return s.failRun(ctx, runID, "revision_plan_invalid")
		}
	}
	// 每次推进都刷新租约：worker 崩溃后由 ResetStaleRuns 回收。
	if _, err := s.db.ExecContext(ctx, `UPDATE generation_runs SET status='running', updated_at=now()
		WHERE id=$1 AND status IN ('queued','running')`, runID); err != nil {
		return err
	}

	for i := 0; i < runStageTries; i++ {
		// 取消可能发生在剧本等慢步骤期间：每轮都重新读取 run 状态，取消后不再派发新阶段。
		var runStatus string
		if err := s.db.QueryRowContext(ctx, `SELECT status FROM generation_runs WHERE id=$1`, runID).Scan(&runStatus); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if runStatus != "queued" && runStatus != "running" {
			return nil
		}

		var projectOwner, projectStatus, keyvisual, finalURL string
		var finalRun sql.NullString
		err := s.db.QueryRowContext(ctx, `SELECT user_id,status,COALESCE(keyvisual_url,''),COALESCE(final_url,''),final_run_id
			FROM projects WHERE id=$1`, projectID).Scan(&projectOwner, &projectStatus, &keyvisual, &finalURL, &finalRun)
		if errors.Is(err, sql.ErrNoRows) {
			return s.failRun(ctx, runID, "project_missing")
		}
		if err != nil {
			return err
		}
		if projectOwner != userID {
			return s.failRun(ctx, runID, "project_changed")
		}
		var userStatus string
		if err := s.db.QueryRowContext(ctx, `SELECT status FROM users WHERE id=$1`, userID).Scan(&userStatus); err != nil {
			return s.failRun(ctx, runID, "user_missing")
		}
		if userStatus != "active" {
			return s.failRun(ctx, runID, "account_inactive")
		}

		// 有在途 job 时等待执行器，不重复派发。
		var active bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs
			WHERE project_id=$1 AND status IN ('queued','running','reconcile'))`, projectID).Scan(&active); err != nil {
			return err
		}
		if active {
			// 已有待结算 receipt 且余额足够时，重新认领原 job，避免 run 卡在 running。
			s.maybeRequeueSettlement(ctx, runID)
			var running, unknown int
			if err := s.db.QueryRowContext(ctx, `SELECT
				count(*) FILTER (WHERE status IN ('queued','running')),
				count(*) FILTER (WHERE status='reconcile' AND error_code='submission_unknown')
				FROM jobs WHERE project_id=$1`, projectID).Scan(&running, &unknown); err != nil {
				return err
			}
			if running == 0 {
				if unknown > 0 {
					// 未知提交只允许核对原任务，不能自动重发，也不能无限占用 running。
					return s.failRun(ctx, runID, "submission_unknown")
				}
				s.parkExistingSettlement(ctx, runID)
			}
			return nil
		}

		if err := s.adoptRunArtifacts(ctx, runID, projectID); err != nil {
			return err
		}
		if err := s.db.QueryRowContext(ctx, `SELECT status,COALESCE(keyvisual_url,'') FROM projects WHERE id=$1`, projectID).Scan(&projectStatus, &keyvisual); err != nil {
			return err
		}

		stage := projectStatus
		switch stage {
		case "BUDGET_EXCEEDED", "FAILED":
			stage, err = s.recoveryStage(ctx, projectID, keyvisual, finalURL, finalRun, runID)
			if err != nil {
				return err
			}
		}

		switch stage {
		case "DRAFT":
			if _, _, err := s.GenerateScript(projectID, nil); err != nil {
				return s.failRun(ctx, runID, "script_failed")
			}
			continue
		case "SCRIPT_GENERATING":
			// 剧本生成有独立租约：仍有效就等待，过期则由本次重新认领。
			var live bool
			if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(script_lease_until>now(),false)
				FROM projects WHERE id=$1`, projectID).Scan(&live); err != nil {
				return err
			}
			if live {
				return nil
			}
			if _, _, err := s.GenerateScript(projectID, nil); err != nil {
				return s.failRun(ctx, runID, "script_failed")
			}
			continue
		case "SCRIPT_REVIEW":
			if keyvisual == "" {
				// 同一 run 已经终态失败过定妆照时不再无限重试；新的授权（新 run）才允许重试。
				var kStatus, kRun string
				e := s.db.QueryRowContext(ctx, `SELECT status,COALESCE(run_id,'') FROM jobs
					WHERE project_id=$1 AND kind='keyvisual' ORDER BY created_at DESC LIMIT 1`, projectID).Scan(&kStatus, &kRun)
				if e == nil && kStatus == "failed" && kRun == runID {
					return s.failRun(ctx, runID, "keyvisual_failed")
				}
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return e
				}
				if _, err := s.EnqueueJobForRun(runID, JobKeyVisual, projectID); err != nil {
					herr := s.handleAdvanceError(ctx, runID, "keyvisual", err)
					if errors.Is(herr, errRecheckRun) {
						continue
					}
					return herr
				}
				continue
			}
			hasShots, err := s.projectHasShots(ctx, projectID)
			if err != nil {
				return err
			}
			if !hasShots {
				return s.failRun(ctx, runID, "shots_missing")
			}
			if _, _, err := s.queueShots(projectID, userID, nil, false, false, runID); err != nil {
				herr := s.handleAdvanceError(ctx, runID, "shots", err)
				if errors.Is(herr, errRecheckRun) {
					continue
				}
				return herr
			}
			continue
		case "KEYVISUAL_GENERATING":
			// 没有在途 job 却停在生成中：存在上游 ID 时只允许核对，不能重新提交付费任务。
			var task string
			if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(keyvisual_task_id,'') FROM projects WHERE id=$1`, projectID).Scan(&task); err != nil {
				return err
			}
			if task != "" {
				return s.failRun(ctx, runID, "keyvisual_recovery_required")
			}
			// 从未提交过付费任务（例如取消清理/入队崩溃）：新授权可以安全重新入队。
			if _, err := s.EnqueueJobForRun(runID, JobKeyVisual, projectID); err != nil {
				herr := s.handleAdvanceError(ctx, runID, "keyvisual", err)
				if errors.Is(herr, errRecheckRun) {
					continue
				}
				return herr
			}
			continue
		case "KEYVISUAL_REVIEW":
			if keyvisual == "" {
				return s.failRun(ctx, runID, "keyvisual_missing")
			}
			hasShots, err := s.projectHasShots(ctx, projectID)
			if err != nil {
				return err
			}
			if !hasShots {
				return s.failRun(ctx, runID, "shots_missing")
			}
			if _, _, err := s.queueShots(projectID, userID, nil, false, false, runID); err != nil {
				herr := s.handleAdvanceError(ctx, runID, "shots", err)
				if errors.Is(herr, errRecheckRun) {
					continue
				}
				return herr
			}
			continue
		case "SHOTS_GENERATING", "SHOTS_REVIEW":
			if err := s.advanceShotStage(ctx, runID, projectID, userID); err != nil {
				if errors.Is(err, errRecheckRun) {
					continue
				}
				return err
			}
			continue
		case "ASSEMBLING":
			// 合成是本地零上游费用步骤；没有在途 job 时可安全重新入队，已有本次交付则收口。
			if finalURL != "" && finalRun.Valid && finalRun.String == runID {
				return s.finishRun(ctx, runID, "completed", "")
			}
			if _, err := s.EnqueueJobForRun(runID, JobAssemble, projectID); err != nil {
				herr := s.handleAdvanceError(ctx, runID, "assemble", err)
				if errors.Is(herr, errRecheckRun) {
					continue
				}
				return herr
			}
			continue
		case "COMPLETED":
			if finalURL == "" {
				return s.failRun(ctx, runID, "final_missing")
			}
			if !finalRun.Valid || finalRun.String != runID {
				// 旧版本成片不能算作本次执行交付；必须由本 run 的合成任务发布。
				return s.failRun(ctx, runID, "final_not_from_run")
			}
			return s.finishRun(ctx, runID, "completed", "")
		default:
			return s.failRun(ctx, runID, "unknown_stage")
		}
	}
	return s.failRun(ctx, runID, "stage_loop")
}

// recoveryStage 用已存素材/job 推导历史项目的可恢复阶段，不重置旧费用或上游身份。
func (s *Service) recoveryStage(ctx context.Context, projectID, keyvisual, finalURL string, finalRun sql.NullString, runID string) (string, error) {
	if finalURL != "" && finalRun.Valid && finalRun.String == runID {
		return "COMPLETED", nil
	}
	var characters, shots int
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM characters WHERE project_id=$1),
		(SELECT count(*) FROM shots WHERE project_id=$1)`, projectID).Scan(&characters, &shots); err != nil {
		return "", err
	}
	if keyvisual != "" {
		return "SHOTS_GENERATING", nil
	}
	if characters > 0 {
		return "SCRIPT_REVIEW", nil
	}
	return "DRAFT", nil
}

func (s *Service) projectHasShots(ctx context.Context, projectID string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM shots WHERE project_id=$1`, projectID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// advanceShotStage 只恢复允许重试的失败镜头，复用已完成素材；无法恢复时给明确原因。
func (s *Service) advanceShotStage(ctx context.Context, runID, projectID, userID string) error {
	// 取消/崩溃后没有上游身份的 QUEUED 镜头可以安全回到待生成；有上游 ID 的必须核对。
	if _, err := s.db.ExecContext(ctx, `UPDATE shots SET status='PENDING',updated_at=now()
		WHERE project_id=$1 AND status='QUEUED' AND COALESCE(upstream_id,'')=''`, projectID); err != nil {
		return err
	}
	var total, done, failedRetry, failedTerminal, manualRequired, pending, queued int
	err := s.db.QueryRowContext(ctx, `SELECT
		count(*),
		count(*) FILTER (WHERE status IN ('GENERATED','APPROVED') AND COALESCE(video_url,'')<>''),
		count(*) FILTER (WHERE status='FAILED' AND retry_count < $2),
		count(*) FILTER (WHERE status='FAILED' AND retry_count >= $2),
		count(*) FILTER (WHERE status='MANUAL_REQUIRED'),
		count(*) FILTER (WHERE status='PENDING'),
		count(*) FILTER (WHERE status='QUEUED')
		FROM shots WHERE project_id=$1`, projectID, ShotMaxRetry).
		Scan(&total, &done, &failedRetry, &failedTerminal, &manualRequired, &pending, &queued)
	if err != nil {
		return err
	}
	if manualRequired > 0 {
		return s.failRun(ctx, runID, "submission_unknown")
	}
	if failedTerminal > 0 {
		return s.failRun(ctx, runID, "shot_failed")
	}
	if queued > 0 {
		// 无在途 job 却仍 QUEUED：只允许核对原任务，不能重新付费提交。
		return s.failRun(ctx, runID, "shot_recovery_required")
	}
	if pending > 0 || failedRetry > 0 {
		if _, _, err := s.queueShots(projectID, userID, nil, false, false, runID); err != nil {
			return s.handleAdvanceError(ctx, runID, "shots", err)
		}
		return nil
	}
	if total == 0 {
		return s.failRun(ctx, runID, "shots_missing")
	}
	if done < total {
		return s.failRun(ctx, runID, "shots_stuck")
	}
	// 合成已经终态失败：同一 run 不无限重试；新授权允许复用素材重试合成。
	var aStatus, aRun string
	e := s.db.QueryRowContext(ctx, `SELECT status,COALESCE(run_id,'') FROM jobs
		WHERE project_id=$1 AND kind='assemble' ORDER BY created_at DESC LIMIT 1`, projectID).Scan(&aStatus, &aRun)
	if e == nil && aStatus == "failed" && aRun == runID {
		return s.failRun(ctx, runID, "assemble_failed")
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	// 全自动：结果已交付的镜头直接通过，不再要求客户逐镜头确认。
	if _, err := s.db.ExecContext(ctx, `UPDATE shots SET status='APPROVED',updated_at=now()
		WHERE project_id=$1 AND status='GENERATED' AND COALESCE(video_url,'')<>''`, projectID); err != nil {
		return err
	}
	if _, err := s.EnqueueJobForRun(runID, JobAssemble, projectID); err != nil {
		return s.handleAdvanceError(ctx, runID, "assemble", err)
	}
	return nil
}

// handleAdvanceError 区分「余额不足→进入等待充值」与真正的阶段失败。
func (s *Service) handleAdvanceError(ctx context.Context, runID, stage string, err error) error {
	var pre *QueuePreflightError
	if errors.As(err, &pre) {
		if pre.Code == "balance_insufficient" {
			required := pre.Required
			if required < 1 {
				required = 1
			}
			e := s.runstore.MarkWaitingCreditsIfShort(ctx, runID, "balance", required)
			switch {
			case errors.Is(e, runpkg.ErrWaitingNotNeeded):
				// 并发充值已把余额补足：让编排重新计算并继续，不留下等待态。
				return errRecheckRun
			case errors.Is(e, runpkg.ErrRunNotActive), errors.Is(e, runpkg.ErrNotFound):
				return nil
			case e != nil:
				return e
			}
			s.Log("", "", "", "run_waiting_credits", stage, "waiting_credits",
				fmt.Sprintf("run=%s required=%d available=%d", runID, required, pre.Available))
			return nil
		}
		return s.failRun(ctx, runID, "budget_insufficient")
	}
	if errors.Is(err, ErrRunNotAuthorized) || errors.Is(err, ErrRunManaged) {
		// 取消/替代与入队竞争失败：不是阶段错误，run 已被取消时保持终态。
		var status string
		if e := s.db.QueryRowContext(ctx, `SELECT status FROM generation_runs WHERE id=$1`, runID).Scan(&status); e == nil && status != "queued" && status != "running" {
			return nil
		}
	}
	// 数据库/事务类错误不把 run 判死：job 与预占已整体回滚，下一次推进可安全重试。
	// 业务终态错误在各阶段分支已显式 failRun，这里保留可恢复性。
	log.Printf("[run] advance %s stage=%s (retryable): %v", runID, stage, err)
	return fmt.Errorf("%s: %w", stage, err)
}

var errRecheckRun = errors.New("funds changed; re-evaluate stage")

func (s *Service) failRun(ctx context.Context, runID, reason string) error {
	reason = truncate(reason, 32)
	if _, err := s.db.ExecContext(ctx, `UPDATE generation_runs
		SET status='failed', waiting_reason=$2, updated_at=now()
		WHERE id=$1 AND status IN ('queued','running')`, runID, reason); err != nil {
		return err
	}
	log.Printf("[run] failed run=%s reason=%s", runID, reason)
	return nil
}

func (s *Service) finishRun(ctx context.Context, runID, status, reason string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE generation_runs
		SET status=$2, waiting_reason=$3,
		    completed_at=CASE WHEN $2::varchar='completed' THEN now() ELSE completed_at END,
		    updated_at=now()
		WHERE id=$1 AND status IN ('queued','running')`, runID, status, truncate(reason, 32)); err != nil {
		return err
	}
	return nil
}

// AdvanceActiveRuns 由 worker 周期调用：推进所有已授权且未结束的执行。
func (s *Service) AdvanceActiveRuns(ctx context.Context, limit int) int {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	s.resumeTerminalSettlements(ctx, limit)
	// 等待补偿：入账与等待写入交错时，outbox 可能已被消费；这里按同一授权与余额口径恢复。
	s.ResumeFundedWaits(ctx, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM generation_runs
		WHERE status IN ('queued','running') AND auto_resume
		ORDER BY updated_at LIMIT $1`, limit)
	if err != nil {
		log.Printf("[run] list active: %v", err)
		return 0
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		if err := s.AdvanceRun(ctx, id); err != nil {
			log.Printf("[run] advance %s: %v", id, err)
			continue
		}
		n++
	}
	return n
}

// ResetStaleRuns 回收崩溃 worker 留下的 running 租约，回到 queued 由下一次推进恢复。
// 不改变 waiting_credits / 终态，也不触碰未授权的项目。
func (s *Service) ResetStaleRuns(ctx context.Context, age time.Duration) int {
	res, err := s.db.ExecContext(ctx, `UPDATE generation_runs
		SET status='queued', waiting_reason='recovered', updated_at=now()
		WHERE status='running' AND updated_at < now()-$1::interval`,
		fmt.Sprintf("%d seconds", int(age.Seconds())))
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// ResumeFromWakeups 消费充值到账后的唤醒记录。返回处理条数。
func (s *Service) ResumeFromWakeups(ctx context.Context, limit int) (int, error) {
	if n, err := s.runstore.ReclaimStale(ctx, 10*time.Minute); err == nil && n > 0 {
		log.Printf("[resume] reclaimed %d stale wakeups", n)
	}
	if _, err := s.runstore.BackfillMissing(ctx); err != nil {
		log.Printf("[resume] backfill: %v", err)
	}
	ws, err := s.runstore.ClaimDue(ctx, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, w := range ws {
		done, err := s.resumeOneWakeup(ctx, w)
		if err != nil {
			log.Printf("[resume] wakeup=%s run=%s: %v", w.ID, w.RunID, err)
			if e := s.runstore.MarkFailed(ctx, w.ID, w.LeaseToken, truncate(err.Error(), 500)); e != nil {
				log.Printf("[resume] mark failed %s: %v", w.ID, e)
			}
			continue
		}
		if done {
			if e := s.runstore.MarkDone(ctx, w.ID, w.LeaseToken); e != nil {
				log.Printf("[resume] mark done %s: %v", w.ID, e)
			}
			processed++
		}
	}
	return processed, nil
}

func (s *Service) resumeOneWakeup(ctx context.Context, w runpkg.Wakeup) (bool, error) {
	// 订单事实必须仍成立：已入账、绑定同一执行、同一用户。
	var orderRun, orderUser, orderStatus string
	var credited bool
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(run_id,''),user_id,status,credited
		FROM recharge_orders WHERE id=$1`, w.OrderID).Scan(&orderRun, &orderUser, &orderStatus, &credited)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil // 订单已不存在，唤醒无意义
	}
	if err != nil {
		return false, err
	}
	if orderRun != w.RunID || orderUser != w.UserID || orderStatus != "paid" || !credited {
		log.Printf("[resume] stale wakeup run=%s order=%s status=%s credited=%v", w.RunID, w.OrderID, orderStatus, credited)
		return true, nil
	}

	reason, err := s.runstore.ResumeRunChecked(ctx, w.RunID, w.UserID)
	if err != nil {
		return false, err
	}
	switch reason {
	case "resumed":
		// 待结算恢复必须重新认领原 job/receipt，而不是新建生成任务。
		s.requeueSettlementJobs(ctx, w.RunID)
		// 续跑信号已持久化；推进失败会由周期扫描恢复，不让唤醒无限重试。
		if e := s.AdvanceRun(ctx, w.RunID); e != nil {
			log.Printf("[resume] advance %s: %v", w.RunID, e)
		}
		return true, nil
	case "insufficient", "not_waiting", "cancelled", "superseded", "account_inactive",
		"project_changed", "project_missing", "auto_resume_off", "no_estimate", "owner_mismatch", "missing":
		log.Printf("[resume] skip run=%s reason=%s", w.RunID, reason)
		return true, nil
	default:
		return true, nil
	}
}

// errJobCancelled 表示提交前发现执行已取消/失效，job 已被安全取消，未产生上游请求。
var errJobCancelled = errors.New("job cancelled before provider submission")
var errAccountPaused = errors.New("账号已停用，未提交任务保持等待")

// authorizeSubmissionTx 是 ready -> submitting 的原子授权边界。
// 取消与这里共用 run-auth advisory 锁：取消先提交则这里必然看到 cancelled。
func (s *Service) authorizeSubmissionTx(tx *sql.Tx, j *Job) error {
	// The user row is already locked by jobMutation; this covers legacy jobs too.
	var accountStatus string
	if err := tx.QueryRow(`SELECT u.status FROM users u JOIN projects p ON p.user_id=u.id WHERE p.id=$1`, j.ProjectID).Scan(&accountStatus); err != nil {
		return err
	}
	if accountStatus != "active" {
		return errAccountPaused
	}
	if j.RunID == "" {
		return nil
	}
	var project, user, status, userStatus, projectOwner string
	var version int
	var autoResume bool
	err := tx.QueryRow(`SELECT project_id,user_id,status,project_version,auto_resume
		FROM generation_runs WHERE id=$1`, j.RunID).
		Scan(&project, &user, &status, &version, &autoResume)
	if errors.Is(err, sql.ErrNoRows) {
		return s.cancelUnsubmittedJobTx(tx, j, "run_missing")
	}
	if err != nil {
		return err
	}
	reason := ""
	if project != j.ProjectID || (status != "queued" && status != "running") || !autoResume {
		reason = "run_inactive"
	} else if err := tx.QueryRow(`SELECT user_id FROM projects WHERE id=$1`, project).Scan(&projectOwner); err != nil || projectOwner != user {
		reason = "project_changed"
	} else if err := tx.QueryRow(`SELECT status FROM users WHERE id=$1`, user).Scan(&userStatus); err != nil || userStatus != "active" {
		reason = "account_inactive"
	} else {
		var newest int
		if err := tx.QueryRow(`SELECT COALESCE(max(project_version),0) FROM generation_runs WHERE project_id=$1`, project).Scan(&newest); err != nil {
			return err
		}
		if newest != version {
			reason = "run_superseded"
		}
	}
	if reason == "" {
		return nil
	}
	return s.cancelUnsubmittedJobTx(tx, j, reason)
}

// cancelUnsubmittedJobTx 只取消确定尚未提交的 job，并只释放一次预占。
func (s *Service) cancelUnsubmittedJobTx(tx *sql.Tx, j *Job, reason string) error {
	if err := settlementExecOne(tx, "cancel unsubmitted job", `UPDATE jobs
		SET status='cancelled',reserved_credits=0,error_code=$3,last_error='执行已取消或失效，未提交上游',updated_at=now()
		WHERE id=$1 AND lease_token=$2 AND status='running'`, j.ID, j.LeaseToken, reason); err != nil {
		return err
	}
	if j.Kind == JobShot {
		if err := settlementExecOne(tx, "cancel queued shot", `UPDATE shots
			SET status='PENDING',upstream_id=NULL,error_msg=NULL,updated_at=now()
			WHERE id=$1 AND project_id=$2 AND status='QUEUED' AND COALESCE(upstream_id,'')=''`, j.ShotID, j.ProjectID); err != nil {
			return err
		}
	} else if j.Kind == JobKeyVisual {
		if err := settlementExecOne(tx, "cancel keyvisual", `UPDATE projects
			SET status='SCRIPT_REVIEW',keyvisual_task_id=NULL,updated_at=now()
			WHERE id=$1 AND status='KEYVISUAL_GENERATING' AND COALESCE(keyvisual_url,'')=''`, j.ProjectID); err != nil {
			return err
		}
	}
	return errJobCancelled
}

// requeueSettlementJobs 只重新认领「已有可信 receipt 的待结算 job」，
// 不重新生成、不改变上游身份，settlement 仍走原幂等路径。
// parkExistingSettlement 把「有可信 receipt 但余额不足」的 run 明确置为等待结算。
func (s *Service) parkExistingSettlement(ctx context.Context, runID string) {
	var user string
	if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM generation_runs WHERE id=$1`, runID).Scan(&user); err != nil {
		return
	}
	f, err := s.runstore.FundingForSettlement(ctx, user, runID)
	if err != nil || f.JobID == "" {
		return
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE generation_runs SET status='waiting_credits', waiting_reason='settlement',
 required_credits=$2,updated_at=now() WHERE id=$1 AND status='running'`, runID, f.Required); err != nil {
		log.Printf("[resume] park settlement run=%s: %v", runID, err)
	}
}

// Requeue one verified receipt, retaining every other reservation. The next
// scheduler cycle will reevaluate the next receipt against the remaining funds.
func (s *Service) maybeRequeueSettlement(ctx context.Context, runID string) {
	var user string
	if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM generation_runs WHERE id=$1`, runID).Scan(&user); err != nil {
		return
	}
	f, err := s.runstore.FundingForSettlement(ctx, user, runID)
	if err != nil || f.JobID == "" || f.Available < f.Required {
		return
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE jobs SET status='queued',run_after=now(),error_code='',updated_at=now()
 WHERE id=$1 AND status='reconcile'`, f.JobID); err != nil {
		log.Printf("[resume] requeue %s: %v", f.JobID, err)
	}
}

func (s *Service) requeueSettlementJobs(ctx context.Context, runID string) {
	s.maybeRequeueSettlement(ctx, runID)
}

// ResumeFundedWaits 是有界的等待补偿扫描：只恢复已有 run 授权、仍有效、
// 且可用积分已覆盖 required_credits 的执行；不启动历史无 run 项目。
func (s *Service) ResumeFundedWaits(ctx context.Context, limit int) int {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.user_id,r.waiting_reason FROM generation_runs r
 JOIN projects p ON p.id=r.project_id AND p.user_id=r.user_id JOIN users u ON u.id=r.user_id AND u.status='active'
 WHERE r.status='waiting_credits' AND r.auto_resume AND r.required_credits>0
 AND NOT EXISTS(SELECT 1 FROM generation_runs n WHERE n.project_id=r.project_id AND n.project_version>r.project_version)
 ORDER BY r.updated_at,r.id LIMIT $1`, limit)
	if err != nil {
		return 0
	}
	type waiting struct{ id, user, reason string }
	var list []waiting
	for rows.Next() {
		var w waiting
		if rows.Scan(&w.id, &w.user, &w.reason) == nil {
			list = append(list, w)
		}
	}
	rows.Close()
	n := 0
	for _, w := range list {
		reason, err := s.runstore.ResumeRunChecked(ctx, w.id, w.user)
		if err != nil || reason != "resumed" {
			continue
		}
		if w.reason == "settlement" {
			s.requeueSettlementJobs(ctx, w.id)
		}
		if err := s.AdvanceRun(ctx, w.id); err != nil {
			log.Printf("[resume] funded advance %s: %v", w.id, err)
			continue
		}
		n++
	}
	return n
}

// A terminal authorization cannot start new work, but verified debts must still
// settle after a later top-up. Inactive waiting accounts get receipt-only recovery too.
// Eligibility is financial, never a run resurrection or new provider submission.
func (s *Service) resumeTerminalSettlements(ctx context.Context, limit int) {
	_, err := s.db.ExecContext(ctx, `WITH due AS (
 SELECT j.id FROM jobs j JOIN generation_runs r ON r.id=j.run_id
 JOIN projects p ON p.id=j.project_id AND p.user_id=r.user_id JOIN users u ON u.id=r.user_id
 JOIN generation_settlements g ON g.provider='limeng' AND g.kind=CASE WHEN j.kind='shot' THEN 'video' ELSE 'image' END
 AND g.upstream_id=j.upstream_id AND g.project_id=j.project_id AND COALESCE(g.shot_id,'')=COALESCE(j.shot_id,'') AND g.user_id=r.user_id AND g.status='pending'
 WHERE j.status='reconcile' AND j.phase='polling' AND COALESCE(j.upstream_id,'')<>''
 AND (r.status IN ('cancelled','failed','completed') OR (u.status<>'active' AND r.status='waiting_credits'))
 AND g.credits<=u.credits-COALESCE((SELECT sum(o.reserved_credits) FROM jobs o JOIN projects op ON op.id=o.project_id WHERE op.user_id=u.id AND o.id<>j.id),0)
 AND g.credits<=p.budget_credits-p.spent_credits-COALESCE((SELECT sum(o.reserved_credits) FROM jobs o WHERE o.project_id=p.id AND o.id<>j.id),0)
 ORDER BY j.updated_at,j.id LIMIT $1
 ) UPDATE jobs SET status='queued',run_after=now(),error_code='',updated_at=now()
 WHERE id IN(SELECT id FROM due) AND status='reconcile'`, limit)
	if err != nil {
		log.Printf("[resume] terminal receipts: %v", err)
	}
}
