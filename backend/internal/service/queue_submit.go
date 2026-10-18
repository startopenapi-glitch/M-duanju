package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type QueuePreflightError struct {
	Code      string `json:"code"`
	Required  int64  `json:"requiredCredits"`
	Available int64  `json:"availableCredits"`
}

func (e *QueuePreflightError) Error() string {
	return fmt.Sprintf("%s：需要%d积分，可用%d积分", e.Code, e.Required, e.Available)
}

var (
	// ErrRunNotAuthorized 表示执行已取消、被替代、账号失效或版本不一致，不能再派发新 job。
	ErrRunNotAuthorized = errors.New("执行授权已失效，不能继续派发新任务")
	// ErrRunManaged 表示项目已有自动执行，旧手动入口不得绕过它直接入队。
	ErrRunManaged = errors.New("该项目由自动执行管理，旧接口不能直接入队")
)

func (s *Service) EnqueueJob(kind, project, shot string, seq int, payload string) (string, error) {
	return s.enqueueJob(kind, project, shot, seq, payload, "")
}

// EnqueueJobForRun 是自动执行专用入口：job、预占与 run 关联在同一事务提交。
func (s *Service) EnqueueJobForRun(runID, kind, project string) (string, error) {
	return s.enqueueJob(kind, project, "", 0, "", runID)
}

func (s *Service) enqueueJob(kind, project, shot string, seq int, payload, runID string) (string, error) {
	if kind == JobShot {
		_, ids, err := s.queueShots(project, "", []int{seq}, false, false, runID)
		if len(ids) > 0 {
			return ids[0], err
		}
		return "", err
	}
	if kind != JobKeyVisual && kind != JobAssemble {
		return "", errors.New("unknown job kind")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	runUser, err := authorizeRunTx(tx, project, runID)
	if err != nil {
		return "", err
	}
	uid, bal, budget, spent, err := lockQueueProject(tx, project)
	if err != nil {
		return "", err
	}
	if runID != "" && runUser != uid {
		return "", ErrRunNotAuthorized
	}
	if err != nil {
		return "", err
	}
	var scriptActive bool
	if err = tx.QueryRow(`SELECT status='SCRIPT_GENERATING' FROM projects WHERE id=$1`, project).Scan(&scriptActive); err != nil {
		return "", err
	}
	if scriptActive {
		return "", errors.New("剧本正在生成，请等待完成")
	}
	var existing string
	err = tx.QueryRow(`SELECT id FROM jobs WHERE project_id=$1 AND kind=$2 AND status IN ('queued','running','reconcile')`, project, kind).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err = ensureGenerationPhase(tx, project, kind); err != nil {
		return "", err
	}
	cost := int64(0)
	task, phase := "", "ready"
	if kind == JobKeyVisual {
		cost = 30
		var status string
		if err = tx.QueryRow(`SELECT COALESCE(keyvisual_task_id,''),status FROM projects WHERE id=$1`, project).Scan(&task, &status); err != nil {
			return "", err
		}
		if task != "" {
			phase = "polling"
		} else if status == "KEYVISUAL_GENERATING" {
			return "", ErrSubmissionUnknown
		}
	} else {
		var invalid, total int
		if err = tx.QueryRow(`SELECT count(*) FILTER(WHERE status<>'APPROVED' OR COALESCE(video_url,'')=''),count(*) FROM shots WHERE project_id=$1`, project).Scan(&invalid, &total); err != nil {
			return "", err
		}
		if invalid > 0 || total == 0 {
			return "", errors.New("全部分镜有视频并通过审片后才能合成")
		}
	}
	if task == "" {
		if err = checkQueueFunds(tx, uid, project, bal, budget, spent, cost); err != nil {
			return "", err
		}
	}
	if payload == "" {
		payload = "{}"
	}
	var id string
	err = tx.QueryRow(`INSERT INTO jobs(kind,project_id,payload,upstream_id,phase,reserved_credits,run_id)
		VALUES($1,$2,$3::jsonb,$4,$5,$6,$7) RETURNING id`, kind, project, payload, nullIfEmpty(task), phase, cost, nullIfEmpty(runID)).Scan(&id)
	if err != nil {
		return "", err
	}
	if kind == JobKeyVisual {
		_, err = tx.Exec(`UPDATE projects SET status='KEYVISUAL_GENERATING',error_msg=NULL,updated_at=now() WHERE id=$1`, project)
	}
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}
func lockQueueProject(tx *sql.Tx, project string) (uid string, bal, budget, spent int64, err error) {
	if err = tx.QueryRow(`SELECT user_id FROM projects WHERE id=$1 AND deleted_at IS NULL`, project).Scan(&uid); err != nil {
		return
	}
	if err = tx.QueryRow(`SELECT credits FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&bal); err != nil {
		return
	}
	var owner string
	err = tx.QueryRow(`SELECT user_id,budget_credits,spent_credits FROM projects WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, project).Scan(&owner, &budget, &spent)
	if err == nil && owner != uid {
		err = errors.New("project owner changed")
	}
	return
}
func checkQueueFunds(tx *sql.Tx, uid, project string, bal, budget, spent, cost int64) error {
	var userReserved, projectReserved int64
	if err := tx.QueryRow(`SELECT COALESCE(sum(j.reserved_credits),0),COALESCE(sum(j.reserved_credits) FILTER(WHERE j.project_id=$2),0) FROM jobs j JOIN projects p ON p.id=j.project_id WHERE p.user_id=$1`, uid, project).Scan(&userReserved, &projectReserved); err != nil {
		return err
	}
	if cost > budget-spent-projectReserved {
		return &QueuePreflightError{"budget_insufficient", cost, budget - spent - projectReserved}
	}
	if cost > bal-userReserved {
		return &QueuePreflightError{"balance_insufficient", cost, bal - userReserved}
	}
	return nil
}

// override 只允许「单镜头 + 用户手动确认」这一条路径，批量与 API 入口一律传 false。
func (s *Service) queueShots(project, caller string, seqs []int, manual bool, override bool, runID string) (int, []string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	runUser, err := authorizeRunTx(tx, project, runID)
	if err != nil {
		return 0, nil, err
	}
	uid, bal, budget, spent, err := lockQueueProject(tx, project)
	if err != nil {
		return 0, nil, err
	}
	if caller != "" && caller != uid {
		return 0, nil, errors.New("project owner mismatch")
	}
	if runID != "" && runUser != uid {
		return 0, nil, ErrRunNotAuthorized
	}
	reject := func(e error) (int, []string, error) {
		if ce := tx.Commit(); ce != nil {
			return 0, nil, ce
		}
		return 0, nil, e
	}
	if err = ensureGenerationPhase(tx, project, JobShot); err != nil {
		return 0, nil, err
	}
	var keyvisual string
	if err = tx.QueryRow(`SELECT COALESCE(keyvisual_url,'') FROM projects WHERE id=$1`, project).Scan(&keyvisual); err != nil {
		return 0, nil, err
	}
	rows, err := tx.Query(`SELECT id,seq,duration,status,retry_count,COALESCE(upstream_id,''),COALESCE(video_url,''),COALESCE(delivered_upstream_id,'') FROM shots WHERE project_id=$1 ORDER BY seq FOR UPDATE`, project)
	if err != nil {
		return 0, nil, err
	}
	type target struct {
		id                   string
		seq                  int
		duration             float64
		status               string
		retry                int
		task, url, delivered string
	}
	var selected []target
	found := map[int]bool{}
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.id, &t.seq, &t.duration, &t.status, &t.retry, &t.task, &t.url, &t.delivered); err != nil {
			rows.Close()
			return 0, nil, err
		}
		match := len(seqs) == 0
		for _, seq := range seqs {
			if t.seq == seq {
				match = true
				found[seq] = true
			}
		}
		if match {
			selected = append(selected, t)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, nil, err
	}
	for _, seq := range seqs {
		if !found[seq] {
			return 0, nil, errors.New("分镜不存在")
		}
	}
	type planned struct {
		target
		cost int64
	}
	var plans []planned
	var total int64
	ids := []string{}
	for _, t := range selected {
		if err = protectLegacyShot(tx, uid, project, t.id, t.task, t.status, t.url, t.delivered); err != nil {
			return 0, nil, err
		}
		var active string
		e := tx.QueryRow(`SELECT id FROM jobs WHERE kind='shot' AND project_id=$1 AND shot_id=$2 AND status IN ('queued','running','reconcile')`, project, t.id).Scan(&active)
		if e == nil {
			if manual {
				return reject(errors.New("已有生成任务，请先恢复查询和结算"))
			}
			continue
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return 0, nil, e
		}
		if t.status == "QUEUED" {
			return reject(errors.New("已有生成任务，请先恢复查询和结算"))
		}
		if !manual && (t.status == "GENERATED" || t.status == "APPROVED") {
			continue
		}
		// MANUAL_REQUIRED 与「已达重试上限」原来是两个硬阻塞，导致用户既不能重试也没有出口。
		// 允许「用户手动确认 + 单镜头」这一条路径放行，并清零重试计数；批量/自动路径仍被拒绝。
		if t.status == "MANUAL_REQUIRED" || t.retry >= ShotMaxRetry {
			if !manual || !override {
				if t.status == "MANUAL_REQUIRED" {
					return reject(ErrSubmissionUnknown)
				}
				return reject(fmt.Errorf("已达重试上限 %d 次", ShotMaxRetry))
			}
			if _, err = tx.Exec(`UPDATE shots SET retry_count=0, updated_at=now() WHERE id=$1`, t.id); err != nil {
				return 0, nil, err
			}
			t.retry = 0
		}
		c := EstimateShotCost(t.duration, getenvDefault("VIDEO_RESOLUTION", "720P"))
		total += c
		plans = append(plans, planned{t, c})
	}
	if len(plans) == 0 {
		return 0, ids, tx.Commit()
	}
	if keyvisual == "" {
		return reject(errors.New("缺少定妆照，请先生成并确认"))
	}
	if err = checkQueueFunds(tx, uid, project, bal, budget, spent, total); err != nil {
		return reject(err)
	}
	for _, p := range plans {
		var id string
		payload, _ := json.Marshal(map[string]string{"resolution": getenvDefault("VIDEO_RESOLUTION", "720P")})
		if err = tx.QueryRow(`INSERT INTO jobs(kind,project_id,shot_id,seq,reserved_credits,payload,run_id)
			VALUES('shot',$1,$2,$3,$4,$5::jsonb,$6) RETURNING id`, project, p.id, p.seq, p.cost, string(payload), nullIfEmpty(runID)).Scan(&id); err != nil {
			return 0, nil, err
		}
		retry := 0
		if manual || p.status == "FAILED" {
			retry = 1
		}
		if _, err = tx.Exec(`UPDATE shots SET status='QUEUED',upstream_id=NULL,retry_count=retry_count+$1,error_msg=NULL,updated_at=now() WHERE id=$2`, retry, p.id); err != nil {
			return 0, nil, err
		}
		ids = append(ids, id)
	}
	if _, err = tx.Exec(`UPDATE projects SET status='SHOTS_GENERATING',error_msg=NULL,updated_at=now() WHERE id=$1`, project); err != nil {
		return 0, nil, err
	}
	return len(ids), ids, tx.Commit()
}

// authorizeRunTx 在同一事务里核验自动执行授权；旧接口在自动项目上直接拒绝。
// 取消与入队/提交共用同一 advisory 锁，保证「取消成功后不再取得提交许可」。
func authorizeRunTx(tx *sql.Tx, project, runID string) (string, error) {
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "run-auth:"+project); err != nil {
		return "", err
	}
	if runID == "" {
		var active bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM generation_runs
			WHERE project_id=$1 AND status IN ('queued','running','waiting_credits','settlement_pending'))`, project).Scan(&active); err != nil {
			return "", err
		}
		if active {
			return "", ErrRunManaged
		}
		return "", nil
	}
	var user, runProject, status, userStatus string
	var version int
	var autoResume bool
	err := tx.QueryRow(`SELECT user_id,project_id,status,project_version,auto_resume
		FROM generation_runs WHERE id=$1`, runID).
		Scan(&user, &runProject, &status, &version, &autoResume)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRunNotAuthorized
	}
	if err != nil {
		return "", err
	}
	if runProject != project || (status != "queued" && status != "running") || !autoResume {
		return "", ErrRunNotAuthorized
	}
	if err := tx.QueryRow(`SELECT status FROM users WHERE id=$1`, user).Scan(&userStatus); err != nil || userStatus != "active" {
		return "", ErrRunNotAuthorized
	}
	var newest int
	if err := tx.QueryRow(`SELECT COALESCE(max(project_version),0) FROM generation_runs WHERE project_id=$1`, project).Scan(&newest); err != nil {
		return "", err
	}
	if newest != version {
		return "", ErrRunNotAuthorized
	}
	return user, nil
}

// One project may run multiple shots, but paid phases cannot invalidate each other.
// Call only while holding the shared user -> project lock.
func ensureGenerationPhase(tx *sql.Tx, project, kind string) error {
	var conflict bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM jobs WHERE project_id=$1 AND kind<>$2 AND status IN ('queued','running','reconcile'))
 OR EXISTS(SELECT 1 FROM projects WHERE id=$1 AND (status='SCRIPT_GENERATING' OR ($2<>'keyvisual' AND (status='KEYVISUAL_GENERATING' OR COALESCE(keyvisual_task_id,'')<>'')) OR ($2<>'assemble' AND status='ASSEMBLING')))
 OR ($2<>'shot' AND EXISTS(SELECT 1 FROM shots WHERE project_id=$1 AND status='QUEUED'))`, project, kind).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return errors.New("项目已有其他阶段任务，请先完成或恢复原任务")
	}
	return nil
}
