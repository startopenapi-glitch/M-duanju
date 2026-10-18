package partner

import (
	"context"
	"database/sql"
	"errors"
)

// SnapshotOrder 在下单事务内固化来源，供两个充值入口（微信 CreatePayOrder / 人工 CreateOrder）共用。
// 语义：
//   - 无归属 → 不写快照，返回 nil
//   - 已有快照 → 保留原快照（重开支付窗口、pending 复用、换链接都不改变订单归属）
//   - 快照记录下单时的代理状态，仅用于统计；暂停代理不影响老客户继续充值
//   - 数据库异常向上抛，不得吞掉
func (s *Store) SnapshotOrder(ctx context.Context, tx *sql.Tx, orderID, userID string) error {
	var partnerID, status string
	err := tx.QueryRowContext(ctx, `SELECT a.partner_id, p.status
	 FROM partner_attributions a JOIN partners p ON p.id = a.partner_id
	 WHERE a.user_id = $1`, userID).Scan(&partnerID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO partner_order_attributions(id,order_id,partner_id,partner_status_at_pay)
	 VALUES($1,$2,$3,$4) ON CONFLICT (order_id) DO NOTHING`, newID(), orderID, partnerID, status)
	return err
}

// OrderSnapshot 读取某笔订单的来源快照；无快照返回空串。
func (s *Store) OrderSnapshot(ctx context.Context, orderID string) (partnerID, statusAtPay string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT partner_id, partner_status_at_pay
	 FROM partner_order_attributions WHERE order_id=$1`, orderID).Scan(&partnerID, &statusAtPay)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return partnerID, statusAtPay, err
}
