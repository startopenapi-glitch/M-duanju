package partner

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// 首次有效推广来源保留 30 天（cookie 有效期）；归属本身是永久的。
const CandidateTTL = 30 * 24 * time.Hour

const CandidateCookie = "partner_candidate"

// BindOnRegister 必须与建号在同一事务内调用。
// 语义：
//   - code 为空 / 无效 → 返回 nil，不阻断注册（来源无效不是注册失败的理由）
//   - 代理已暂停 → 返回 ErrSuspended，不写归属；调用方应忽略该错误继续建号
//   - 已有归属 → 不覆盖（首次有效来源优先，A/B 竞争由唯一索引兜底）
//   - 数据库异常 → 返回错误，调用方必须让它导致整体回滚，不得吞掉伪装绑定成功
//
// 与 SetStatus 的并发顺序由行锁保证：FOR SHARE 会等待进行中的 UPDATE 提交，
// 因此「暂停」与「绑定」不会同时读到 active 并都成功。
func (s *Store) BindOnRegister(ctx context.Context, tx *sql.Tx, userID, code string) error {
	if code == "" {
		return nil
	}
	var partnerID, status string
	err := tx.QueryRowContext(ctx, `SELECT id,status FROM partners WHERE code=$1 FOR SHARE`, code).
		Scan(&partnerID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != StatusActive {
		return ErrSuspended
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO partner_attributions(id,user_id,partner_id,code,source)
	 VALUES($1,$2,$3,$4,'link') ON CONFLICT (user_id) DO NOTHING`, newID(), userID, partnerID, code)
	return err
}

// AttributedPartnerID 返回该用户的归属代理；没有归属返回空串。
func (s *Store) AttributedPartnerID(ctx context.Context, userID string) (string, error) {
	var pid string
	err := s.db.QueryRowContext(ctx, `SELECT partner_id FROM partner_attributions WHERE user_id=$1`, userID).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return pid, err
}
