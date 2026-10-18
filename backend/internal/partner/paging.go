package partner

import (
	"context"
	"strings"
	"time"

	"drama-platform/internal/model"
)

func partnerPaging(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return page, size
}

// ListPage is the bounded administrator projection; legacy List remains compatible.
func (s *Store) ListPage(ctx context.Context, kw, status string, page, size int) (*model.PageResp, error) {
	page, size = partnerPaging(page, size)
	where := ` FROM partners WHERE ($1='' OR name ILIKE $2 OR code ILIKE $2 OR user_id ILIKE $2) AND ($3='' OR status=$3)`
	args := []any{strings.TrimSpace(kw), "%" + strings.TrimSpace(kw) + "%", status}
	var total int
	if e := s.db.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT `+partnerCols+where+` ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`, append(args, size, int64(page-1)*int64(size))...)
	if e != nil {
		return nil, e
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
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return &model.PageResp{Items: out, Total: total, Page: page, PageSize: size}, nil
}

func (s *Store) CustomersPage(ctx context.Context, id string, page, size int) (*model.PageResp, error) {
	page, size = partnerPaging(page, size)
	var total int
	if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM partner_attributions WHERE partner_id=$1`, id).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT a.user_id,a.attributed_at,
 EXISTS(SELECT 1 FROM projects p WHERE p.user_id=a.user_id),
 EXISTS(SELECT 1 FROM generation_settlements g WHERE g.user_id=a.user_id AND g.status='settled'),
 EXISTS(SELECT 1 FROM recharge_orders o WHERE o.user_id=a.user_id AND o.status='paid' AND NOT o.mock AND o.refunded_at IS NULL AND COALESCE(o.refund_amount_cents,0)=0)
 FROM partner_attributions a WHERE a.partner_id=$1 ORDER BY a.attributed_at DESC,a.user_id DESC LIMIT $2 OFFSET $3`, id, size, int64(page-1)*int64(size))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Customer{}
	for rows.Next() {
		var uid string
		var at time.Time
		var c Customer
		if e = rows.Scan(&uid, &at, &c.HasProject, &c.HasGenerated, &c.HasPaid); e != nil {
			return nil, e
		}
		c.Alias = alias(id, uid)
		c.RegisteredAt = at.UTC().Format(time.RFC3339)
		out = append(out, c)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return &model.PageResp{Items: out, Total: total, Page: page, PageSize: size}, nil
}

func (s *Store) OrdersPage(ctx context.Context, id string, page, size int) (*model.PageResp, error) {
	page, size = partnerPaging(page, size)
	var total int
	const from = ` FROM partner_order_attributions pa JOIN recharge_orders o ON o.id=pa.order_id WHERE pa.partner_id=$1 AND (o.status='paid' OR o.refunded_at IS NOT NULL)`
	if e := s.db.QueryRowContext(ctx, `SELECT count(*)`+from, id).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT o.id,o.amount_yuan,COALESCE(o.channel,''),o.status,o.created_at,COALESCE(o.refund_amount_cents,0),(o.refunded_at IS NOT NULL)`+from+` ORDER BY o.created_at DESC,o.id DESC LIMIT $2 OFFSET $3`, id, size, int64(page-1)*int64(size))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []OrderRow{}
	for rows.Next() {
		var oid string
		var amount float64
		var cents int64
		var refunded bool
		var at time.Time
		var o OrderRow
		if e = rows.Scan(&oid, &amount, &o.Channel, &o.Status, &at, &cents, &refunded); e != nil {
			return nil, e
		}
		o.MaskedID = maskOrderID(oid)
		o.AmountYuan = strings.TrimRight(strings.TrimRight(formatYuan(amount), "0"), ".")
		o.CreatedAt = at.UTC().Format(time.RFC3339)
		if refunded || cents > 0 {
			o.RefundHint = "有退款记录，结果待核对"
		}
		out = append(out, o)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return &model.PageResp{Items: out, Total: total, Page: page, PageSize: size}, nil
}
