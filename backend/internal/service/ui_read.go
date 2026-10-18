package service

import (
	"context"
	"database/sql"
	runpkg "drama-platform/internal/run"
	"encoding/base64"
	"errors"
	qrcode "github.com/skip2/go-qrcode"
	"net/url"
	"regexp"
	"time"
)

var ErrUIReadNotFound = errors.New("记录不存在或无权访问")
var ErrUIReadInvalid = errors.New("查询参数无效")
var uiRunID = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)
var uiRecordID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type RunContextQuery struct{ RunID, ProjectID, RequestKey string }
type RunProject struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}
type RunContext struct {
	Run                     *runpkg.Run `json:"run"`
	Project                 RunProject  `json:"project"`
	IsLatest                bool        `json:"isLatest"`
	HasUnresolvedSubmission bool        `json:"hasUnresolvedSubmission"`
	AvailableCredits        int64       `json:"availableCredits"`
	ShortfallCredits        int64       `json:"shortfallCredits"`
	ObservedAt              string      `json:"observedAt"`
}

func (s *Service) RunReadContext(ctx context.Context, userID string, q RunContextQuery) (*RunContext, error) {
	if q.RunID != "" {
		if !uiRunID.MatchString(q.RunID) || q.ProjectID != "" || q.RequestKey != "" {
			return nil, ErrUIReadInvalid
		}
	} else if !uiRecordID.MatchString(q.ProjectID) || q.RequestKey == "" || len(q.RequestKey) > 64 {
		return nil, ErrUIReadInvalid
	}
	var id string
	if q.RunID == "" {
		var allowed bool
		e := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects p JOIN users u ON u.id=p.user_id WHERE p.id=$1 AND p.user_id=$2 AND p.deleted_at IS NULL AND u.status='active')`, q.ProjectID, userID).Scan(&allowed)
		if e != nil {
			return nil, e
		}
		if !allowed {
			return nil, ErrUIReadNotFound
		}
		identity, e := s.runstore.ResolveRequest(ctx, userID, q.RequestKey)
		if errors.Is(e, runpkg.ErrNotFound) {
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		if identity.ProjectID != q.ProjectID {
			return nil, nil
		}
		id = identity.RunID
	} else {
		id = q.RunID
	}
	out := &RunContext{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	e := s.db.QueryRowContext(ctx, `SELECT p.id,p.title,p.status,NOT EXISTS(SELECT 1 FROM generation_runs newer WHERE newer.project_id=p.id AND newer.project_version>r.project_version),EXISTS(SELECT 1 FROM jobs j WHERE j.run_id=r.id AND j.project_id=p.id AND j.error_code='submission_unknown' AND j.status NOT IN ('done','abandoned')) FROM generation_runs r JOIN projects p ON p.id=r.project_id JOIN users u ON u.id=p.user_id WHERE r.id=$1 AND r.user_id=$2 AND p.user_id=$2 AND p.deleted_at IS NULL AND u.status='active'`, id, userID).Scan(&out.Project.ID, &out.Project.Title, &out.Project.Status, &out.IsLatest, &out.HasUnresolvedSubmission)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrUIReadNotFound
	}
	if e != nil {
		return nil, e
	}
	r, e := s.runstore.ByID(ctx, id)
	if e != nil {
		return nil, e
	}
	view, e := s.readRunView(ctx, userID, out.Project.ID, r)
	if e != nil {
		return nil, e
	}
	out.Run = view.Run
	out.AvailableCredits = view.AvailableCredits
	out.ShortfallCredits = view.ShortfallCredits
	return out, nil
}

type OrderRefundProgress struct {
	Status      string `json:"status"`
	AmountCents int64  `json:"amountCents"`
}
type OrderPayment struct {
	QR        string `json:"qr,omitempty"`
	H5URL     string `json:"h5Url,omitempty"`
	ExpiresAt string `json:"expiresAt"`
}
type OrderProgress struct {
	Payment         *OrderPayment        `json:"payment"`
	ID              string               `json:"id"`
	Status          string               `json:"status"`
	PayMode         string               `json:"payMode"`
	AmountCents     int64                `json:"amountCents"`
	Credits         int64                `json:"credits"`
	Credited        bool                 `json:"credited"`
	RunID           *string              `json:"runId"`
	ProjectID       *string              `json:"projectId"`
	PaymentEvidence string               `json:"paymentEvidence"`
	Refund          *OrderRefundProgress `json:"refund"`
	ObservedAt      string               `json:"observedAt"`
}

func (s *Service) OrderProgress(ctx context.Context, userID, orderID string) (*OrderProgress, error) {
	if !uiRecordID.MatchString(orderID) {
		return nil, ErrUIReadInvalid
	}
	o := &OrderProgress{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	var transaction string
	var legacy bool
	var refundAmount int64
	var codeURL, h5URL, channel string
	var createdAt time.Time
	e := s.db.QueryRowContext(ctx, `SELECT o.id,o.status,o.pay_mode,o.amount_cents,o.credits,o.credited,r.id,p.id,COALESCE(o.transaction_id,''),o.refunded_at IS NOT NULL,o.refund_amount_cents,COALESCE(o.code_url,''),COALESCE(o.h5_url,''),COALESCE(o.channel,''),o.created_at FROM recharge_orders o JOIN users u ON u.id=o.user_id LEFT JOIN generation_runs r ON r.id=o.run_id AND r.user_id=o.user_id LEFT JOIN projects p ON p.id=r.project_id AND p.user_id=o.user_id WHERE o.id=$1 AND o.user_id=$2 AND u.status='active'`, orderID, userID).Scan(&o.ID, &o.Status, &o.PayMode, &o.AmountCents, &o.Credits, &o.Credited, &o.RunID, &o.ProjectID, &transaction, &legacy, &refundAmount, &codeURL, &h5URL, &channel, &createdAt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrUIReadNotFound
	}
	if e != nil {
		return nil, e
	}
	o.PaymentEvidence = "unknown"
	if o.PayMode == "manual" {
		o.PaymentEvidence = "manual"
	} else if o.PayMode == "wechat" && transaction != "" {
		o.PaymentEvidence = "recorded"
	}
	var refund OrderRefundProgress
	var unverified bool
	e = s.db.QueryRowContext(ctx, `SELECT status,amount_cents,legacy_unverified FROM refund_intents WHERE order_id=$1`, orderID).Scan(&refund.Status, &refund.AmountCents, &unverified)
	if e == nil {
		if unverified {
			refund.Status = "legacy_unverified"
		}
		o.Refund = &refund
	} else if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	} else if legacy {
		o.Refund = &OrderRefundProgress{Status: "legacy_unverified", AmountCents: refundAmount}
	}
	// Restore presentation from the existing authorized order only. No channel
	// calls, new order, credit write or change to payment state occurs here.
	age := time.Since(createdAt)
	if o.Status == "pending" && o.PayMode == "wechat" && !o.Credited && transaction == "" && o.Refund == nil && age >= 0 {
		u, err := url.Parse(codeURL)
		if channel == "native" && age < 30*time.Minute && err == nil && u.Scheme == "weixin" && u.Host == "wxpay" && u.Path == "/bizpayurl" {
			if png, e := qrcode.Encode(codeURL, qrcode.Medium, 512); e == nil {
				o.Payment = &OrderPayment{QR: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), ExpiresAt: createdAt.Add(30 * time.Minute).UTC().Format(time.RFC3339)}
			}
		}
		u, err = url.Parse(h5URL)
		if channel == "h5" && age < 5*time.Minute && err == nil && u.Scheme == "https" && u.Host == "wx.tenpay.com" && u.User == nil {
			o.Payment = &OrderPayment{H5URL: h5URL, ExpiresAt: createdAt.Add(5 * time.Minute).UTC().Format(time.RFC3339)}
		}
	}
	return o, nil
}
