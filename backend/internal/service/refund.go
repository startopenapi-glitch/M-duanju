package service

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrRefundConflict    = errors.New("refund already exists with different amount or reason")
	ErrRefundUnavailable = errors.New("verified refund channel unavailable")
	ErrRefundLegacy      = errors.New("historical refund requires manual reconciliation")
)

type RefundRecord struct {
	ID              string    `json:"id"`
	OrderID         string    `json:"orderId"`
	RefundNo        string    `json:"refundNo"`
	Status          string    `json:"status"`
	AmountCents     int64     `json:"amountCents"`
	CreditsRevoked  int64     `json:"creditsRevoked"`
	CreditShortfall int64     `json:"creditShortfall"`
	Reason          string    `json:"reason"`
	Error           string    `json:"error"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	providerID      string
	adminID         sql.NullString
	legacy          bool
}

// RefundClient has no retry operation: all recovery is GET-only using RefundNo.
type RefundClient interface {
	SubmitRefund(outTradeNo, refundNo string, totalCents, refundCents int64, reason string) (*RefundResponse, error)
	LookupRefund(refundNo string) (*RefundResponse, error)
}
type RefundResponse struct {
	RefundID      string `json:"refund_id"`
	OutRefundNo   string `json:"out_refund_no"`
	OutTradeNo    string `json:"out_trade_no"`
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
	Amount        struct {
		Total    int64  `json:"total"`
		Refund   int64  `json:"refund"`
		Currency string `json:"currency"`
	} `json:"amount"`
}

const refundColumns = `id,order_id,refund_no,status,amount_cents,credits_revoked,credit_shortfall,reason,error,created_at,updated_at,provider_refund_id,admin_id,legacy_unverified`

type refundScanner interface{ Scan(...interface{}) error }

func scanRefund(row refundScanner) (*RefundRecord, error) {
	r := new(RefundRecord)
	err := row.Scan(&r.ID, &r.OrderID, &r.RefundNo, &r.Status, &r.AmountCents, &r.CreditsRevoked, &r.CreditShortfall, &r.Reason, &r.Error, &r.CreatedAt, &r.UpdatedAt, &r.providerID, &r.adminID, &r.legacy)
	return r, err
}
func (s *Service) GetRefund(orderID string) (*RefundRecord, error) {
	r, e := scanRefund(s.db.QueryRow(`SELECT `+refundColumns+` FROM refund_intents WHERE order_id=$1`, orderID))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return r, e
}
func (s *Service) refundTransport() (RefundClient, error) {
	if s.refundClient != nil {
		return s.refundClient, nil
	}
	w := NewWechatPay()
	if w == nil || w.Mock || w.PrivateKey == nil {
		return nil, ErrRefundUnavailable
	}
	return w, nil
}
func (s *Service) BeginRefund(orderID, adminID string, amountCents int64, reason string) (*RefundRecord, error) {
	reason = strings.TrimSpace(reason)
	if amountCents <= 0 || reason == "" || len(reason) > 1000 {
		return nil, fmt.Errorf("%w：请输入正数金额和退款原因", ErrAdminInvalid)
	}
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var uid, status, channel, trade string
	var total int64
	var old sql.NullTime
	var mock, credited bool
	e = tx.QueryRow(`SELECT user_id,status,COALESCE(channel,''),COALESCE(out_trade_no,''),amount_cents,refunded_at,mock,credited FROM recharge_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&uid, &status, &channel, &trade, &total, &old, &mock, &credited)
	if e != nil {
		return nil, e
	}
	existing, e := scanRefund(tx.QueryRow(`SELECT `+refundColumns+` FROM refund_intents WHERE order_id=$1`, orderID))
	if e == nil {
		if existing.legacy {
			return existing, ErrRefundLegacy
		}
		if existing.AmountCents != amountCents || existing.Reason != reason {
			return existing, ErrRefundConflict
		}
		return existing, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if old.Valid {
		return nil, ErrRefundLegacy
	}
	if status != OrderPaid || !credited || amountCents > total {
		return nil, fmt.Errorf("%w：仅可退已付款且已入账订单，金额不可超过原付款", ErrAdminInvalid)
	}
	if mock || (channel != "native" && channel != "h5") || trade == "" {
		return nil, ErrRefundUnavailable
	}
	client, e := s.refundTransport()
	if e != nil {
		return nil, e
	}
	no := "RF" + strings.ReplaceAll(uuid.NewString(), "-", "")
	r, e := scanRefund(tx.QueryRow(`INSERT INTO refund_intents(order_id,refund_no,status,amount_cents,reason,admin_id) VALUES($1,$2,'submitting',$3,$4,$5) RETURNING `+refundColumns, orderID, no, amountCents, reason, adminID))
	if e != nil {
		return nil, e
	}
	if e = refundAudit(tx, adminID, orderID, "refund_requested", "", "submitting", fmt.Sprintf("refundNo=%s amountCents=%d reason=%s", no, amountCents, reason)); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	// This is the only POST. A crash here leaves a queryable submitting intent.
	response, callErr := client.SubmitRefund(trade, no, total, amountCents, reason)
	return s.applyRefundResponse(r.OrderID, adminID, response, callErr)
}
func (s *Service) QueryRefund(orderID, adminID string) (*RefundRecord, error) {
	r, e := s.GetRefund(orderID)
	if e != nil {
		return nil, e
	}
	if r == nil {
		return nil, sql.ErrNoRows
	}
	if r.legacy {
		return r, ErrRefundLegacy
	}
	if r.Status == "succeeded" || r.Status == "failed" {
		return r, nil
	}
	client, e := s.refundTransport()
	if e != nil {
		return r, e
	}
	response, callErr := client.LookupRefund(r.RefundNo)
	return s.applyRefundResponse(orderID, adminID, response, callErr)
}
func refundAudit(tx *sql.Tx, adminID, orderID, action, from, to, note string) error {
	_, e := tx.Exec(`INSERT INTO audit_logs(user_id,action,from_status,to_status,note) VALUES($1,$2,$3,$4,$5)`, adminID, action, from, to, "order="+orderID+" "+note)
	return e
}
func (s *Service) applyRefundResponse(orderID, adminID string, response *RefundResponse, callErr error) (*RefundRecord, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	r, e := scanRefund(tx.QueryRow(`SELECT `+refundColumns+` FROM refund_intents WHERE order_id=$1 FOR UPDATE`, orderID))
	if e != nil {
		return nil, e
	}
	if r.legacy {
		return r, ErrRefundLegacy
	}
	if r.Status == "succeeded" || r.Status == "failed" {
		return r, nil
	}
	var uid, trade, transaction string
	var total, orderCredits int64
	e = tx.QueryRow(`SELECT user_id,COALESCE(out_trade_no,''),COALESCE(transaction_id,''),amount_cents,credits FROM recharge_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&uid, &trade, &transaction, &total, &orderCredits)
	if e != nil {
		return nil, e
	}
	status, message := "unknown", "Channel result could not be verified; query this refund before further action"
	valid := callErr == nil && response != nil && response.RefundID != "" && response.OutRefundNo == r.RefundNo && response.OutTradeNo == trade && response.Amount.Total == total && response.Amount.Refund == r.AmountCents && response.Amount.Currency == "CNY" && (transaction == "" || response.TransactionID == transaction) && (r.providerID == "" || r.providerID == response.RefundID)
	if valid {
		switch response.Status {
		case "SUCCESS":
			status = "succeeded"
			message = ""
		case "PROCESSING":
			status = "processing"
			message = ""
		case "CLOSED":
			status = "failed"
			message = "Refund closed by channel"
		case "ABNORMAL":
			status = "unknown"
			message = "Channel reports abnormal refund; reconciliation required"
		}
		r.providerID = response.RefundID
	}
	if status == "succeeded" {
		var balance, reserved int64
		if e = tx.QueryRow(`SELECT credits FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&balance); e != nil {
			return nil, e
		}
		if e = tx.QueryRow(`SELECT COALESCE(sum(j.reserved_credits),0) FROM jobs j JOIN projects p ON p.id=j.project_id WHERE p.user_id=$1`, uid).Scan(&reserved); e != nil {
			return nil, e
		}
		// Queue reservations are created under the same user row lock. Only the
		// unreserved balance is reclaimable; shortfall is a record, never debt.
		available := balance - reserved
		if available < 0 {
			available = 0
		}
		// Supported recharge tiers award more credits than cash cents. Revoke
		// the proportional credits for partial refunds, not the cash amount.
		want := refundCredits(r.AmountCents, total, orderCredits)
		revoke := want
		if revoke > available {
			revoke = available
		}
		r.CreditsRevoked = revoke
		r.CreditShortfall = want - revoke
		if _, e = tx.Exec(`UPDATE users SET credits=credits-$1,updated_at=now() WHERE id=$2`, revoke, uid); e != nil {
			return nil, e
		}
		if _, e = tx.Exec(`INSERT INTO credit_transactions(user_id,amount,balance_after,kind,note,operator_id) VALUES($1,$2,$3,'refund',$4,$5)`, uid, -revoke, balance-revoke, fmt.Sprintf("refund=%s amountCents=%d creditShortfall=%d", r.RefundNo, r.AmountCents, r.CreditShortfall), r.adminID); e != nil {
			return nil, e
		}
		if _, e = tx.Exec(`UPDATE recharge_orders SET refund_amount_cents=$1,refund_reason=$2,refunded_at=now(),refund_no=$3,updated_at=now() WHERE id=$4`, r.AmountCents, r.Reason, r.RefundNo, orderID); e != nil {
			return nil, e
		}
	}
	if e = refundAudit(tx, adminID, orderID, "refund_result", r.Status, status, fmt.Sprintf("refundNo=%s amountCents=%d creditsRevoked=%d creditShortfall=%d %s", r.RefundNo, r.AmountCents, r.CreditsRevoked, r.CreditShortfall, message)); e != nil {
		return nil, e
	}
	r, e = scanRefund(tx.QueryRow(`UPDATE refund_intents SET status=$1,error=$2,provider_refund_id=$3,credits_revoked=$4,credit_shortfall=$5,updated_at=now() WHERE order_id=$6 RETURNING `+refundColumns, status, message, r.providerID, r.CreditsRevoked, r.CreditShortfall, orderID))
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return r, nil
}

// refundCredits maps a cash refund to the credits originally granted by the
// order. For the standard 1:1 order it equals amountCents; for bonus tiers it
// revokes the proportional bonus as well.
func refundCredits(amountCents, orderAmountCents, orderCredits int64) int64 {
	if orderAmountCents > 0 && orderCredits > 0 {
		return (amountCents*orderCredits + orderAmountCents/2) / orderAmountCents
	}
	return amountCents
}

// RefundOrder retains the old call shape without ever equating acceptance with success.
type RefundRequest struct {
	OrderID     string
	AmountCents int64
	Reason      string
	AdminID     string
}

func (s *Service) RefundOrder(req *RefundRequest) (string, error) {
	if req == nil {
		return "", errors.New("refund request required")
	}
	amount := req.AmountCents
	if amount == 0 {
		o, e := s.GetOrder(req.OrderID)
		if e != nil {
			return "", e
		}
		amount = o.AmountCents
	}
	r, e := s.BeginRefund(req.OrderID, req.AdminID, amount, req.Reason)
	if e != nil {
		return "", e
	}
	if r.Status != "succeeded" {
		return r.RefundNo, fmt.Errorf("refund %s; query existing refund", r.Status)
	}
	return r.RefundNo, nil
}
