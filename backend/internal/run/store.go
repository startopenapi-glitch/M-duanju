// Package run 管理一次生成执行（run）的持久化意图与幂等解析。
// 它不替换既有 jobs：一个 run 派生多个 job，复用既有租约、预占与结算。
package run

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrIdempotencyConflict = errors.New("同一幂等键对应了不同的生成输入")
	ErrRunActive           = errors.New("该项目已有进行中的生成执行")
	ErrNotFound            = errors.New("生成执行不存在")
	ErrRunNotActive        = errors.New("执行已结束，不能进入等待态")
	// ErrWaitingNotNeeded 表示等待写入时余额已被补足（并发入账先提交），
	// 调用方必须重新计算阶段，而不是把本次执行留在等待态。
	ErrWaitingNotNeeded = errors.New("余额已足够，无需进入等待")
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

func newID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

type CreateReq struct {
	UserID           string
	ProjectID        string
	Mode             string // first | revise
	IdempotencyKey   string
	InputHash        string
	Scope            string // JSON
	Pricing          string // JSON
	CreditsEstimated int64
}

type Run struct {
	ID               string `json:"id"`
	UserID           string `json:"-"`
	ProjectID        string `json:"projectId"`
	ProjectVersion   int    `json:"projectVersion"`
	Mode             string `json:"mode"`
	Status           string `json:"status"`
	Checkpoint       string `json:"checkpoint"`
	WaitingReason    string `json:"waitingReason,omitempty"`
	AutoResume       bool   `json:"autoResume"`
	CreditsEstimated int64  `json:"creditsEstimated"`
	RequiredCredits  int64  `json:"requiredCredits"`
	CreditsSettled   int64  `json:"creditsSettled"`
	RequestedAt      string `json:"requestedAt"`
}

const cols = `id,user_id,project_id,project_version,mode,status,checkpoint,waiting_reason,
 auto_resume,credits_estimated,required_credits,credits_settled,requested_at`

func scan(row interface{ Scan(...any) error }) (*Run, error) {
	var r Run
	var at time.Time
	if e := row.Scan(&r.ID, &r.UserID, &r.ProjectID, &r.ProjectVersion, &r.Mode, &r.Status,
		&r.Checkpoint, &r.WaitingReason, &r.AutoResume, &r.CreditsEstimated, &r.RequiredCredits, &r.CreditsSettled, &at); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, e
	}
	r.RequestedAt = at.UTC().Format(time.RFC3339)
	return &r, nil
}

// Create 幂等创建执行意图。
//   - 同用户同幂等键 + 同输入哈希 → 返回既有 run（重复点击/双标签页不会重复付费）
//   - 同键但输入不同 → ErrIdempotencyConflict（绝不能覆盖旧输入）
//   - 同项目同输入的活动 run → 原子保存新请求键并复用；输入不同 → ErrRunActive
func (s *Store) Create(ctx context.Context, req CreateReq) (*Run, error) {
	return s.CreateWithSetup(ctx, req, nil)
}

// CreateWithSetup commits the domain setup with a newly inserted intent. Replays
// do not repeat setup. A setup failure leaves no rechargeable orphan run.
func (s *Store) CreateWithSetup(ctx context.Context, req CreateReq, setup func(*sql.Tx, string) error) (*Run, error) {
	if req.UserID == "" || req.ProjectID == "" || req.IdempotencyKey == "" {
		return nil, errors.New("缺少必要参数")
	}
	if req.Mode != "first" && req.Mode != "revise" {
		req.Mode = "first"
	}
	if req.Scope == "" {
		req.Scope = "{}"
	}
	if req.Pricing == "" {
		req.Pricing = "{}"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err = lockRequestProject(ctx, tx, req.UserID, req.ProjectID, req.IdempotencyKey); err != nil {
		return nil, err
	}
	existing, err := requestRun(ctx, tx, req.UserID, req.ProjectID, req.IdempotencyKey, req.InputHash)
	if err == nil {
		return existing, tx.Commit()
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	var activeID, activeUser, activeHash string
	err = tx.QueryRowContext(ctx, `SELECT id,user_id,input_hash FROM generation_runs
	 WHERE project_id=$1 AND status IN ('queued','running','waiting_credits','settlement_pending')`,
		req.ProjectID).Scan(&activeID, &activeUser, &activeHash)
	if err == nil {
		if activeUser != req.UserID || activeHash != req.InputHash {
			return nil, ErrRunActive
		}
		if err = insertRequest(ctx, tx, req.UserID, req.IdempotencyKey, activeID); err != nil {
			return nil, err
		}
		r, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM generation_runs WHERE id=$1`, activeID))
		if err != nil {
			return nil, err
		}
		return r, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var version int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(project_version),0)+1 FROM generation_runs
	 WHERE project_id=$1`, req.ProjectID).Scan(&version); err != nil {
		return nil, err
	}

	id := newID()
	if _, err = tx.ExecContext(ctx, `INSERT INTO generation_runs
	 (id,user_id,project_id,project_version,mode,status,idempotency_key,input_hash,scope,pricing_snapshot,credits_estimated)
	 VALUES($1,$2,$3,$4,$5,'queued',$6,$7,$8::jsonb,$9::jsonb,$10)`,
		id, req.UserID, req.ProjectID, version, req.Mode, req.IdempotencyKey, req.InputHash,
		req.Scope, req.Pricing, req.CreditsEstimated); err != nil {
		return nil, err
	}
	if err = insertRequest(ctx, tx, req.UserID, req.IdempotencyKey, id); err != nil {
		return nil, err
	}
	if setup != nil {
		if err = setup(tx, id); err != nil {
			return nil, err
		}
	}
	r, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM generation_runs WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return r, tx.Commit()
}

// MarkWaitingCredits 记录「积分不足、等待充值」——不丢输入、不丢已完成素材。
// required 是恢复本执行还需要的总积分，不是本次缺口；缺口由可用积分实时计算。
func (s *Store) MarkWaitingCredits(ctx context.Context, runID, reason string, required int64) error {
	// 只允许「进行中」的执行进入等待态：已取消/已完成/已结算的不得被复活。
	res, err := s.db.ExecContext(ctx, `UPDATE generation_runs
	 SET status='waiting_credits', waiting_reason=$2, required_credits=$3, updated_at=now()
	 WHERE id=$1 AND status IN ('queued','running')`, runID, reason, required)
	if err != nil {
		return err
	}
	if n, e := res.RowsAffected(); e == nil && n == 0 {
		return ErrRunNotActive
	}
	return nil
}

// MarkWaitingCreditsIfShort 只在「仍不足」时写入等待态。
//
// 竞态要点：入账事务会先锁 users 行。这里在写入等待后再次锁 users 并重算可用积分；
// 若期间已有充值提交，则把 run 退回原状态并返回 ErrWaitingNotNeeded，让编排继续推进，
// 避免「充值已到账、唤醒已消费、随后又写成等待」的永久丢失。
func (s *Store) MarkWaitingCreditsIfShort(ctx context.Context, runID, reason string, required int64) error {
	if required <= 0 {
		return errors.New("缺少可恢复的总需求")
	}
	var status, userID string
	if err := s.db.QueryRowContext(ctx, `SELECT status,user_id FROM generation_runs WHERE id=$1`, runID).
		Scan(&status, &userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status != "queued" && status != "running" {
		return ErrRunNotActive
	}
	previous := status

	availableSQL := func(placeholder string) string {
		return `(SELECT u.credits - COALESCE((SELECT sum(j.reserved_credits)
		FROM jobs j JOIN projects p ON p.id=j.project_id
		WHERE p.user_id=u.id AND j.status IN ('queued','running','reconcile')),0)
		FROM users u WHERE u.id=` + placeholder + `)`
	}
	// 第一步只写等待事实并立即提交，避免持有 run 行锁再去等 users 行锁，
	// 与入账事务（users -> resume_wakeups 外键）形成死锁。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE generation_runs
		SET status='waiting_credits', waiting_reason=$2, required_credits=$3, updated_at=now()
		WHERE id=$1 AND status IN ('queued','running') AND $3 > `+availableSQL("$4"),
		runID, reason, required, userID)
	if err != nil {
		tx.Rollback()
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		tx.Rollback()
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if n == 0 {
		return ErrWaitingNotNeeded
	}

	// 第二步在独立事务里与入账串行化：若等待写入期间已充值，回退等待态并让编排继续。
	tx2, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx2.Rollback()
	if err = tx2.QueryRowContext(ctx, `SELECT status FROM generation_runs WHERE id=$1`, runID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrWaitingNotNeeded
		}
		return err
	}
	if status != "waiting_credits" {
		return ErrWaitingNotNeeded // 取消/恢复已先行，交给调用方重算
	}
	var credits int64
	if err = tx2.QueryRowContext(ctx, `SELECT credits FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&credits); err != nil {
		return err
	}
	var available int64
	if err = tx2.QueryRowContext(ctx, `SELECT `+availableSQL("$1"), userID).Scan(&available); err != nil {
		return err
	}
	if available >= required {
		if _, err = tx2.ExecContext(ctx, `UPDATE generation_runs
			SET status=$2, waiting_reason='', required_credits=0, updated_at=now()
			WHERE id=$1 AND status='waiting_credits'`, runID, previous); err != nil {
			return err
		}
		if err = tx2.Commit(); err != nil {
			return err
		}
		return ErrWaitingNotNeeded
	}
	return tx2.Commit()
}

func (s *Store) ByID(ctx context.Context, id string) (*Run, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM generation_runs WHERE id=$1`, id))
}

// ActiveByProject 返回项目当前进行中的执行（含等待态）；没有时返回 ErrNotFound。
func (s *Store) ActiveByProject(ctx context.Context, projectID string) (*Run, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM generation_runs
		WHERE project_id=$1 AND status IN ('queued','running','waiting_credits','settlement_pending')
		ORDER BY project_version DESC LIMIT 1`, projectID))
}

// InputHash 返回执行的输入指纹，用于判断重复启动是否同一范围。
func (s *Store) InputHash(ctx context.Context, runID string) (string, error) {
	var h string
	if err := s.db.QueryRowContext(ctx, `SELECT input_hash FROM generation_runs WHERE id=$1`, runID).Scan(&h); err != nil {
		return "", err
	}
	return h, nil
}

// LatestByProject 返回项目最新版本的执行；没有执行时返回 ErrNotFound。
func (s *Store) LatestByProject(ctx context.Context, projectID string) (*Run, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM generation_runs
		WHERE project_id=$1 ORDER BY project_version DESC, requested_at DESC LIMIT 1`, projectID))
}
