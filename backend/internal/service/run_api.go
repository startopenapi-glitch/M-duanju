package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	runpkg "drama-platform/internal/run"
)

// StartRunReq 是「点击开始生成」的服务端入口参数。
// 不接受客户端提交的扣费金额或余额；也不接受客户端提供的输入指纹。
type StartRunReq struct {
	IdempotencyKey string          `json:"idempotencyKey"`
	Mode           string          `json:"mode"`  // first | revise
	Scope          json.RawMessage `json:"scope"` // 时长/画幅/风格等制作范围
}

type StartRunResult struct {
	Run               *runpkg.Run `json:"run"`
	AvailableCredits  int64       `json:"availableCredits"`
	EstimatedCredits  int64       `json:"estimatedCredits"`
	ShortfallCredits  int64       `json:"shortfallCredits"`
	WaitingForCredits bool        `json:"waitingForCredits"`
}

// RunNotCreatedError marks only failures proven not to have created a run.
// It changes no charging behavior; clients may correct a rejected revision instead
// of treating it as an ambiguous submitted run. Post-commit read errors never use it.
type RunNotCreatedError struct{ Err error }

func (e *RunNotCreatedError) Error() string { return e.Err.Error() }
func (e *RunNotCreatedError) Unwrap() error { return e.Err }

// StartRun 创建（或幂等复用）一次生成执行，并判定积分是否足以开始。
// 积分不足时仍持久化执行记录并进入 waiting_credits，不阻断、不丢输入。
func (s *Service) StartRun(userID, projectID string, req *StartRunReq) (*StartRunResult, error) {
	if userID == "" || projectID == "" || req == nil {
		return nil, errors.New("缺少必要参数")
	}
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 64 {
		return nil, errors.New("幂等键无效")
	}
	mode := req.Mode
	if mode == "" {
		mode = "first"
	}
	if mode != "first" && mode != "revise" {
		return nil, errors.New("生成模式不支持")
	}
	ctx := context.Background()
	var owner, account string
	var totalSeconds int
	if err := s.db.QueryRow(`SELECT p.user_id,COALESCE(p.total_seconds,0),u.status FROM projects p JOIN users u ON u.id=p.user_id WHERE p.id=$1 AND p.deleted_at IS NULL`, projectID).Scan(&owner, &totalSeconds, &account); err != nil {
		return nil, errors.New("项目不存在")
	}
	if owner != userID || account != "active" {
		return nil, errors.New("无权操作该项目")
	}
	scope := "{}"
	if len(req.Scope) > 0 {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(req.Scope, &obj); err != nil || obj == nil {
			return nil, errors.New("制作范围无效")
		}
		canonical, _ := json.Marshal(obj)
		scope = string(canonical)
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("run.v1|%s|%s|%s|%s", userID, projectID, mode, scope)))
	inputHash := hex.EncodeToString(h[:])
	// Replays never re-plan or re-authorize a completed/cancelled execution.
	identity, e := s.runstore.ResolveRequest(ctx, userID, req.IdempotencyKey)
	if e == nil {
		if identity.ProjectID != projectID || identity.InputHash != inputHash {
			return nil, runpkg.ErrIdempotencyConflict
		}
		r, err := s.runstore.ByID(ctx, identity.RunID)
		if err != nil {
			return nil, err
		}
		return s.startRunView(r)
	}
	if !errors.Is(e, runpkg.ErrNotFound) {
		return nil, e
	}
	if active, e := s.runstore.ActiveByProject(ctx, projectID); e == nil {
		bound, e := s.runstore.BindRequest(ctx, userID, projectID, req.IdempotencyKey, inputHash, active.ID)
		if errors.Is(e, runpkg.ErrRunActive) {
			return nil, &RunNotCreatedError{Err: e}
		}
		if e != nil {
			return nil, e
		}
		return s.startRunView(bound)
	} else if !errors.Is(e, runpkg.ErrNotFound) {
		return nil, e
	}
	resolution := getenvDefault("VIDEO_RESOLUTION", "720P")
	shots := totalSeconds / 5
	if shots < 1 {
		shots = 1
	}
	estimate := int64(shots)*EstimateShotCost(5, resolution) + 30
	var plan *revisionPlan
	if mode == "revise" {
		var err error
		plan, err = s.planRevision(projectID, json.RawMessage(scope), resolution)
		if err != nil {
			return nil, &RunNotCreatedError{Err: err}
		}
		estimate = plan.Estimate
	}
	r, err := s.runstore.CreateWithSetup(ctx, runpkg.CreateReq{UserID: userID, ProjectID: projectID, Mode: mode, IdempotencyKey: req.IdempotencyKey, InputHash: inputHash, Scope: scope,
		Pricing: fmt.Sprintf(`{"resolution":%q,"perShot":%d,"keyvisual":30}`, resolution, EstimateShotCost(5, resolution)), CreditsEstimated: estimate}, func(tx *sql.Tx, id string) error {
		uid, balance, budget, spent, e := lockQueueProject(tx, projectID)
		if e != nil {
			return e
		}
		if uid != userID {
			return ErrRunNotAuthorized
		}
		var status string
		if e = tx.QueryRow(`SELECT status FROM users WHERE id=$1`, userID).Scan(&status); e != nil {
			return e
		}
		if status != "active" {
			return ErrRunNotAuthorized
		}
		if mode == "first" {
			if e = rearmFailedShotsTx(tx, projectID, id); e != nil {
				return e
			}
		}
		if plan != nil {
			if e = s.saveRevisionTx(tx, id, projectID, plan); e != nil {
				return e
			}
		}
		if need := spent + estimate*2; budget < need {
			if _, e = tx.Exec(`UPDATE projects SET budget_credits=$1,updated_at=now() WHERE id=$2`, need, projectID); e != nil {
				return e
			}
		}
		var reserved int64
		if e = tx.QueryRow(`SELECT COALESCE(sum(j.reserved_credits),0) FROM jobs j JOIN projects p ON p.id=j.project_id WHERE p.user_id=$1`, userID).Scan(&reserved); e != nil {
			return e
		}
		if balance-reserved < estimate {
			_, e = tx.Exec(`UPDATE generation_runs SET status='waiting_credits',waiting_reason='balance',required_credits=$2 WHERE id=$1`, id, estimate)
			if e != nil {
				return e
			}
		}
		return nil
	})
	if errors.Is(err, runpkg.ErrRunActive) {
		return nil, &RunNotCreatedError{Err: err}
	}
	if err != nil {
		return nil, err
	}
	return s.startRunView(r)
}

func (s *Service) startRunView(r *runpkg.Run) (*StartRunResult, error) {
	avail, err := s.runstore.AvailableCredits(context.Background(), r.UserID)
	if err != nil {
		return nil, err
	}
	required := r.RequiredCredits
	if required <= 0 {
		required = r.CreditsEstimated
	}
	if r.WaitingReason == "settlement" {
		f, e := s.runstore.FundingForSettlement(context.Background(), r.UserID, r.ID)
		if e != nil {
			return nil, e
		}
		required, avail = f.Required, f.Available
		r.RequiredCredits = required
	}
	out := &StartRunResult{Run: r, AvailableCredits: avail, EstimatedCredits: r.CreditsEstimated, WaitingForCredits: r.Status == "waiting_credits"}
	if out.WaitingForCredits && required > avail {
		out.ShortfallCredits = required - avail
	}
	return out, nil
}

// RunView 是项目工作台需要的执行状态：run 本身 + 当前可用积分与缺口。
type RunView struct {
	Outputs          []RunOutput `json:"outputs"`
	Run              *runpkg.Run `json:"run"`
	AvailableCredits int64       `json:"availableCredits"`
	ShortfallCredits int64       `json:"shortfallCredits"`
}

// LatestRun 返回项目最近一次执行；没有执行时返回 (nil, nil)，供历史项目显示「继续自动生成」。
func (s *Service) LatestRun(userID, projectID string) (*RunView, error) {
	var owner string
	if err := s.db.QueryRow(`SELECT user_id FROM projects WHERE id=$1 AND deleted_at IS NULL`, projectID).Scan(&owner); err != nil {
		return nil, errors.New("项目不存在")
	}
	if owner != userID {
		return nil, errors.New("无权访问该项目")
	}
	r, err := s.runstore.LatestByProject(context.Background(), projectID)
	if errors.Is(err, runpkg.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.readRunView(context.Background(), userID, projectID, r)
}

// readRunView only projects existing run, receipt and balance facts; it never settles.
func (s *Service) readRunView(ctx context.Context, userID, projectID string, r *runpkg.Run) (*RunView, error) {
	avail, err := s.runstore.AvailableCredits(ctx, userID)
	if err != nil {
		return nil, err
	}
	if r.WaitingReason == "settlement" {
		f, e := s.runstore.FundingForSettlement(ctx, userID, r.ID)
		if e != nil {
			return nil, e
		}
		avail = f.Available
		r.RequiredCredits = f.Required
	}
	outputs, err := s.deliveredRunOutputs(userID, projectID)
	if err != nil {
		return nil, err
	}
	// Derive the per-run display from original receipts, not a second balance.
	if err = s.db.QueryRow(`SELECT COALESCE(sum(g.credits),0) FROM generation_settlements g JOIN jobs j ON j.project_id=g.project_id AND j.upstream_id=g.upstream_id
      AND COALESCE(j.shot_id,'')=COALESCE(g.shot_id,'') AND g.kind=CASE WHEN j.kind='shot' THEN 'video' ELSE 'image' END
      WHERE j.run_id=$1 AND g.user_id=$2 AND g.provider='limeng' AND g.status='settled'`, r.ID, userID).Scan(&r.CreditsSettled); err != nil {
		return nil, err
	}
	view := &RunView{Run: r, AvailableCredits: avail, Outputs: outputs}
	required := r.RequiredCredits
	if required <= 0 && r.WaitingReason != "settlement" {
		required = r.CreditsEstimated
	}
	if r.Status == "waiting_credits" && required > avail {
		view.ShortfallCredits = required - avail
	}
	return view, nil
}

// CancelRun 停止继续派发新任务；已提交/未知的上游任务仍由 jobs 核对与结算。
// 取消不等于退款，也不删除已产生的素材与流水。
func (s *Service) CancelRun(userID, projectID, runID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 与入队/提交共用同一把项目级授权锁：取消先提交，后续 ready -> submitting 必然失败。
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "run-auth:"+projectID); err != nil {
		return err
	}
	var status string
	err = tx.QueryRow(`SELECT status FROM generation_runs
		WHERE id=$1 AND user_id=$2 AND project_id=$3 FOR UPDATE`, runID, userID, projectID).Scan(&status)
	if err != nil {
		return errors.New("执行不存在或已结束")
	}
	if status == "cancelled" {
		// A lost successful response can be replayed for this exact owner/run.
		// Do not touch queued work belonging to a later authorization.
		return tx.Commit()
	}
	if status != "queued" && status != "running" && status != "waiting_credits" && status != "settlement_pending" {
		return errors.New("执行不存在或已结束")
	}
	if _, err = tx.Exec(`UPDATE generation_runs
		SET status='cancelled', cancelled_at=now(), waiting_reason='', updated_at=now()
		WHERE id=$1`, runID); err != nil {
		return err
	}
	// 只清理确定尚未提交的 job；已提交/提交未知/待结算的保留查询与结算，不清空上游身份。
	rows, err := tx.Query(`UPDATE jobs
		SET status='cancelled', reserved_credits=0, error_code='run_cancelled',
		    last_error='执行已取消，任务未提交上游', updated_at=now()
		WHERE run_id=$1 AND project_id=$2 AND status='queued' AND phase='ready' AND COALESCE(upstream_id,'')=''
		RETURNING COALESCE(shot_id,''),kind`, runID, projectID)
	if err != nil {
		return err
	}
	var shots []string
	cancelledKinds := map[string]bool{}
	for rows.Next() {
		var shot, kind string
		if err = rows.Scan(&shot, &kind); err != nil {
			rows.Close()
			return err
		}
		cancelledKinds[kind] = true
		if shot != "" {
			shots = append(shots, shot)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, shot := range shots {
		if _, err = tx.Exec(`UPDATE shots SET status='PENDING', upstream_id=NULL, error_msg=NULL, updated_at=now()
			WHERE id=$1 AND project_id=$2 AND status='QUEUED' AND COALESCE(upstream_id,'')=''`, shot, projectID); err != nil {
			return err
		}
	}
	for kind := range cancelledKinds {
		from, to := "", ""
		switch kind {
		case JobKeyVisual:
			from, to = "KEYVISUAL_GENERATING", "SCRIPT_REVIEW"
		case JobAssemble:
			from, to = "ASSEMBLING", "SHOTS_REVIEW"
		default:
			continue
		}
		if _, err = tx.Exec(`UPDATE projects SET status=$3,updated_at=now() WHERE id=$1 AND status=$2
   AND ($4<>'keyvisual' OR COALESCE(keyvisual_task_id,'')='')
   AND NOT EXISTS(SELECT 1 FROM jobs WHERE project_id=$1 AND kind=$4 AND status IN ('queued','running','reconcile'))`, projectID, from, to, kind); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.Log(userID, projectID, "", "cancel_run", "", "cancelled", "run="+runID)
	return nil
}
