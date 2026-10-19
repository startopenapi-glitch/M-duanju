package run

import (
	"context"
	"database/sql"
	"errors"
)

// AvailableCredits 复算「可用积分 = 账面余额 − 其他任务的有效预占」。
//
// 只用于「是否够开始 / 缺口多少」的判断与展示；真正的预占与扣费仍由
// settlement 与 queue_submit 在事务内原子完成。这里不写任何余额、不建第二套钱包。
// 公式与 settlement.go 的 otherUserReserved 汇总保持一致：只统计进行中/已提交但未结算的任务。
func (s *Store) AvailableCredits(ctx context.Context, userID string) (int64, error) {
	return availableCredits(ctx, s.db, userID)
}

// queryRower 同时被 *sql.DB 与 *sql.Tx 满足，保证续跑校验和入账事务看到同一快照。
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func availableCredits(ctx context.Context, q queryRower, userID string) (int64, error) {
	var balance, reserved int64
	err := q.QueryRowContext(ctx, `
		SELECT u.credits,
		       COALESCE((SELECT sum(j.reserved_credits)
		                 FROM jobs j JOIN projects p ON p.id = j.project_id
		                 WHERE p.user_id = u.id
		                   AND j.status IN ('queued','running','reconcile')), 0)
		FROM users u WHERE u.id = $1`, userID).Scan(&balance, &reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	avail := balance - reserved
	if avail < 0 {
		avail = 0
	}
	return avail, nil
}

// SettlementFunding describes the next original receipt to settle. Keeping the
// sibling reservations makes this identical to SettleGeneration's serial debit.
type SettlementFunding struct {
	JobID               string
	Required, Available int64
}

func settlementFunding(ctx context.Context, q queryRower, userID, runID string) (SettlementFunding, error) {
	var f SettlementFunding
	err := q.QueryRowContext(ctx, `WITH candidate AS (
 SELECT j.id,g.credits FROM jobs j JOIN generation_runs r ON r.id=j.run_id
 JOIN generation_settlements g ON g.provider='limeng'
   AND g.kind=CASE WHEN j.kind='shot' THEN 'video' ELSE 'image' END
   AND g.upstream_id=j.upstream_id AND g.project_id=j.project_id
   AND COALESCE(g.shot_id,'')=COALESCE(j.shot_id,'') AND g.user_id=r.user_id
   AND g.status='pending'
 WHERE j.run_id=$2 AND r.user_id=$1 AND j.kind IN ('shot','keyvisual')
   AND j.status IN ('queued','running','reconcile') AND j.phase<>'submitting'
 ORDER BY j.created_at,j.id LIMIT 1
 ) SELECT COALESCE(c.id,''),COALESCE(c.credits,0),GREATEST(0,u.credits-
 COALESCE((SELECT sum(j.reserved_credits) FROM jobs j JOIN projects p ON p.id=j.project_id
 WHERE p.user_id=u.id AND j.id<>COALESCE(c.id,'')),0))
 FROM users u LEFT JOIN candidate c ON true WHERE u.id=$1`, userID, runID).
		Scan(&f.JobID, &f.Required, &f.Available)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return f, err
}

func (s *Store) FundingForSettlement(ctx context.Context, userID, runID string) (SettlementFunding, error) {
	return settlementFunding(ctx, s.db, userID, runID)
}

func (s *Store) AvailableForSettlement(ctx context.Context, userID, runID string) (int64, error) {
	f, err := s.FundingForSettlement(ctx, userID, runID)
	return f.Available, err
}

// Shortfall 返回缺口；足够时返回 0。
func (s *Store) Shortfall(ctx context.Context, userID string, estimate int64) (int64, error) {
	avail, err := s.AvailableCredits(ctx, userID)
	if err != nil {
		return 0, err
	}
	if avail >= estimate {
		return 0, nil
	}
	return estimate - avail, nil
}
