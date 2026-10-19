package partner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// 一期只做运营统计，不把充值本金当利润，也不展示可提现收益。
type Overview struct {
	NewUsers      int64  `json:"newUsers"`
	ProjectUsers  int64  `json:"projectUsers"`
	FirstGenUsers int64  `json:"firstGenUsers"`
	PayingUsers   int64  `json:"payingUsers"`
	DataAsOf      string `json:"dataAsOf"`
	CommissionOn  bool   `json:"commissionOn"` // 一期恒为 false
}

type Customer struct {
	Alias        string `json:"alias"`
	RegisteredAt string `json:"registeredAt"`
	HasProject   bool   `json:"hasProject"`
	HasGenerated bool   `json:"hasGenerated"`
	HasPaid      bool   `json:"hasPaid"`
}

type OrderRow struct {
	MaskedID   string `json:"maskedId"`
	AmountYuan string `json:"amountYuan"`
	Channel    string `json:"channel"`
	Status     string `json:"status"`
	RefundHint string `json:"refundHint,omitempty"`
	CreatedAt  string `json:"createdAt"`
}

// alias 由 partnerID+userID 哈希派生：稳定、不可逆，且不暴露邮箱前缀或昵称。
func alias(partnerID, userID string) string {
	sum := sha256.Sum256([]byte("partner-alias.v1|" + partnerID + "|" + userID))
	h := strings.ToUpper(hex.EncodeToString(sum[:4]))
	return "客户 " + h[:8]
}

func maskOrderID(id string) string {
	if len(id) <= 4 {
		return "****"
	}
	return "****" + id[len(id)-4:]
}

// Customers 用 EXISTS 子查询分别判断三项，避免多表 JOIN 造成行倍增。
func (s *Store) Customers(ctx context.Context, partnerID string) ([]Customer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.user_id, a.attributed_at,
		  EXISTS(SELECT 1 FROM projects p WHERE p.user_id = a.user_id),
		  EXISTS(SELECT 1 FROM generation_settlements g WHERE g.user_id = a.user_id AND g.status = 'settled'),
		  EXISTS(SELECT 1 FROM recharge_orders o WHERE o.user_id = a.user_id AND o.status = 'paid' AND NOT o.mock
		    AND o.refunded_at IS NULL AND COALESCE(o.refund_amount_cents,0) = 0)
		FROM partner_attributions a
		WHERE a.partner_id = $1
		ORDER BY a.attributed_at DESC`, partnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Customer{}
	for rows.Next() {
		var uid string
		var at time.Time
		var proj, gen, paid bool
		if err = rows.Scan(&uid, &at, &proj, &gen, &paid); err != nil {
			return nil, err
		}
		out = append(out, Customer{
			Alias:        alias(partnerID, uid),
			RegisteredAt: at.UTC().Format(time.RFC3339),
			HasProject:   proj,
			HasGenerated: gen,
			HasPaid:      paid,
		})
	}
	return out, rows.Err()
}

func (s *Store) Overview(ctx context.Context, partnerID string) (*Overview, error) {
	cs, err := s.Customers(ctx, partnerID)
	if err != nil {
		return nil, err
	}
	o := &Overview{NewUsers: int64(len(cs)), DataAsOf: time.Now().UTC().Format(time.RFC3339)}
	for _, c := range cs {
		if c.HasProject {
			o.ProjectUsers++
		}
		if c.HasGenerated {
			o.FirstGenUsers++
		}
		if c.HasPaid {
			o.PayingUsers++
		}
	}
	return o, nil
}

// Orders 以订单快照为准（下单时固化），只返回脱敏编号、金额、渠道与本地状态。
func (s *Store) Orders(ctx context.Context, partnerID string, limit int) ([]OrderRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id, o.amount_yuan, COALESCE(o.channel,''), o.status, o.created_at,
		       COALESCE(o.refund_amount_cents,0), (o.refunded_at IS NOT NULL)
		FROM partner_order_attributions pa
		JOIN recharge_orders o ON o.id = pa.order_id
		WHERE pa.partner_id = $1 AND (o.status = 'paid' OR o.refunded_at IS NOT NULL)
		ORDER BY o.created_at DESC
		LIMIT $2`, partnerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OrderRow{}
	for rows.Next() {
		var id, channel, status string
		var amount float64
		var refundCents int64
		var refunded bool
		var at time.Time
		if err = rows.Scan(&id, &amount, &channel, &status, &at, &refundCents, &refunded); err != nil {
			return nil, err
		}
		r := OrderRow{
			MaskedID:   maskOrderID(id),
			AmountYuan: strings.TrimRight(strings.TrimRight(formatYuan(amount), "0"), "."),
			Channel:    channel,
			Status:     status,
			CreatedAt:  at.UTC().Format(time.RFC3339),
		}
		if refunded || refundCents > 0 {
			r.RefundHint = "有退款记录，结果待核对"
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func formatYuan(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// List 管理端列表（一期只读，按开通时间倒序）。
func (s *Store) List(ctx context.Context, limit int) ([]Partner, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+partnerCols+` FROM partners ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Partner{}
	for rows.Next() {
		p, e := scanPartner(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}
