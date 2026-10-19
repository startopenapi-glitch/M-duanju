package partner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"drama-platform/internal/model"
)

const (
	InvitePending  = "pending"
	InviteAccepted = "accepted"
	InviteRevoked  = "revoked"
	InviteExpired  = "expired"

	inviteTTL = 7 * 24 * time.Hour
)

var (
	ErrInviteInvalid  = errors.New("邀请链接无效或已撤销")
	ErrInviteExpired  = errors.New("邀请链接已过期，请联系管理员重新发送")
	ErrInviteUsed     = errors.New("该邀请已被使用，请联系管理员")
	ErrInviteEmail    = errors.New("当前登录邮箱与邀请邮箱不一致")
	ErrInviteName     = errors.New("合作方名称不能为空或过长")
	ErrInviteIntro    = errors.New("简介最多 80 字")
	ErrInviteWechat   = errors.New("客服微信最多 64 字")
	ErrInviteMailUser = errors.New("该邮箱已是合作伙伴")
)

type Invitation struct {
	ID                string `json:"id"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	Intro             string `json:"intro"`
	Wechat            string `json:"wechat"`
	Status            string `json:"status"`
	InvitedBy         string `json:"invitedBy"`
	AcceptedUserID    string `json:"acceptedUserId,omitempty"`
	AcceptedPartnerID string `json:"acceptedPartnerId,omitempty"`
	ExpiresAt         string `json:"expiresAt"`
	SendCount         int    `json:"sendCount"`
	LastSentAt        string `json:"lastSentAt"`
	AcceptedAt        string `json:"acceptedAt,omitempty"`
	CreatedAt         string `json:"createdAt"`
}

type InvitationLookup struct {
	Valid     bool   `json:"valid"`
	Status    string `json:"status"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

const invitationCols = `id,email,name,intro,wechat,status,invited_by,COALESCE(accepted_user_id,''),COALESCE(accepted_partner_id,''),expires_at,send_count,last_sent_at,accepted_at,created_at`

func normalizeInviteEmail(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 255 {
		return "", errors.New("请输入正确的邮箱地址")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || !strings.Contains(addr.Address, "@") {
		return "", errors.New("请输入正确的邮箱地址")
	}
	email := strings.ToLower(strings.TrimSpace(addr.Address))
	if email == "" || len(email) > 255 {
		return "", errors.New("请输入正确的邮箱地址")
	}
	return email, nil
}

func inviteToken() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}

func inviteHash(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

func validateInviteProfile(name, intro, wechat string) (string, string, string, error) {
	name = strings.TrimSpace(name)
	intro = strings.TrimSpace(intro)
	wechat = strings.TrimSpace(wechat)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return "", "", "", ErrInviteName
	}
	if utf8.RuneCountInString(intro) > 80 {
		return "", "", "", ErrInviteIntro
	}
	if utf8.RuneCountInString(wechat) > 64 {
		return "", "", "", ErrInviteWechat
	}
	return name, intro, wechat, nil
}

func scanInvitation(row interface{ Scan(...any) error }) (*Invitation, error) {
	var v Invitation
	var expires, lastSent, created time.Time
	var accepted sql.NullTime
	if err := row.Scan(&v.ID, &v.Email, &v.Name, &v.Intro, &v.Wechat, &v.Status, &v.InvitedBy,
		&v.AcceptedUserID, &v.AcceptedPartnerID, &expires, &v.SendCount, &lastSent, &accepted, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInviteInvalid
		}
		return nil, err
	}
	v.ExpiresAt = expires.UTC().Format(time.RFC3339)
	v.LastSentAt = lastSent.UTC().Format(time.RFC3339)
	v.CreatedAt = created.UTC().Format(time.RFC3339)
	if accepted.Valid {
		v.AcceptedAt = accepted.Time.UTC().Format(time.RFC3339)
	}
	if v.Status == InvitePending && !expires.After(time.Now()) {
		v.Status = InviteExpired
	}
	return &v, nil
}

// CreateInvitation 创建或重发一个待接受邀请。返回原始 token（只发给邮件，不落库）。
func (s *Store) CreateInvitation(ctx context.Context, actorID, rawEmail, name, intro, wechat string) (*Invitation, string, error) {
	email, err := normalizeInviteEmail(rawEmail)
	if err != nil {
		return nil, "", err
	}
	name, intro, wechat, err = validateInviteProfile(name, intro, wechat)
	if err != nil {
		return nil, "", err
	}
	raw, hash, err := inviteToken()
	if err != nil {
		return nil, "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "partner-invite:"+email); err != nil {
		return nil, "", err
	}
	// A direct partner already exists for this mailbox: don't create a dangling invitation.
	var alreadyPartner bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM partners p JOIN users u ON u.id=p.user_id
		WHERE lower(btrim(u.email))=$1)`, email).Scan(&alreadyPartner); err != nil {
		return nil, "", err
	}
	if alreadyPartner {
		return nil, "", ErrInviteMailUser
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM partner_invitations
		WHERE email=$1 AND status='pending' FOR UPDATE`, email).Scan(&existingID)
	if err == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE partner_invitations
			SET token_hash=$2,name=$3,intro=$4,wechat=$5,invited_by=$6,expires_at=now()+$7::interval,
			    send_count=send_count+1,last_sent_at=now(),updated_at=now()
			WHERE id=$1 AND status='pending'`, existingID, hash, name, intro, wechat, actorID, inviteTTL.String()); err != nil {
			return nil, "", err
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		existingID = newID()
		if _, err = tx.ExecContext(ctx, `INSERT INTO partner_invitations
			(id,email,name,intro,wechat,token_hash,status,invited_by,expires_at,last_sent_at)
			VALUES($1,$2,$3,$4,$5,$6,'pending',$7,now()+$8::interval,now())`,
			existingID, email, name, intro, wechat, hash, actorID, inviteTTL.String()); err != nil {
			if isUniqueViolation(err) {
				return nil, "", ErrAlreadyExists
			}
			return nil, "", err
		}
	} else {
		return nil, "", err
	}
	if err = tx.Commit(); err != nil {
		return nil, "", err
	}
	inv, err := s.InvitationByID(ctx, existingID)
	if err != nil {
		return nil, "", err
	}
	return inv, raw, nil
}

func (s *Store) InvitationByID(ctx context.Context, id string) (*Invitation, error) {
	return scanInvitation(s.db.QueryRowContext(ctx, `SELECT `+invitationCols+` FROM partner_invitations WHERE id=$1`, id))
}

// ResendInvitation 轮换 token 和有效期；只有 pending 可重发。
func (s *Store) ResendInvitation(ctx context.Context, actorID, id string) (*Invitation, string, error) {
	raw, hash, err := inviteToken()
	if err != nil {
		return nil, "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM partner_invitations WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrInviteInvalid
		}
		return nil, "", err
	}
	if status != InvitePending {
		return nil, "", ErrInviteUsed
	}
	if _, err = tx.ExecContext(ctx, `UPDATE partner_invitations
		SET token_hash=$2,invited_by=$3,expires_at=now()+$4::interval,send_count=send_count+1,
		    last_sent_at=now(),updated_at=now()
		WHERE id=$1 AND status='pending'`, id, hash, actorID, inviteTTL.String()); err != nil {
		return nil, "", err
	}
	if err = tx.Commit(); err != nil {
		return nil, "", err
	}
	inv, err := s.InvitationByID(ctx, id)
	if err != nil {
		return nil, "", err
	}
	return inv, raw, nil
}

func (s *Store) RevokeInvitation(ctx context.Context, actorID, id string) (*Invitation, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE partner_invitations
		SET status='revoked',updated_at=now()
		WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		inv, e := s.InvitationByID(ctx, id)
		if e != nil {
			return nil, e
		}
		if inv.Status == InviteRevoked {
			return inv, nil
		}
		return nil, ErrInviteUsed
	}
	return s.InvitationByID(ctx, id)
}

// LookupInvitation 只按 token 返回接受页所需字段；token 是邮件里的秘密。
func (s *Store) LookupInvitation(ctx context.Context, raw string) (*InvitationLookup, error) {
	if strings.TrimSpace(raw) == "" || len(raw) > 200 {
		return nil, ErrInviteInvalid
	}
	inv, err := scanInvitation(s.db.QueryRowContext(ctx, `SELECT `+invitationCols+`
		FROM partner_invitations WHERE token_hash=$1`, inviteHash(raw)))
	if err != nil {
		return nil, err
	}
	if inv.Status == InviteAccepted {
		return nil, ErrInviteUsed
	}
	if inv.Status == InviteRevoked {
		return nil, ErrInviteInvalid
	}
	if inv.Status == InviteExpired {
		return nil, ErrInviteExpired
	}
	return &InvitationLookup{
		Valid:     true,
		Status:    inv.Status,
		Email:     inv.Email,
		Name:      inv.Name,
		ExpiresAt: inv.ExpiresAt,
	}, nil
}

// AcceptInvitation 把邀请绑定到当前登录用户；邮箱必须与邀请一致。
// 只开通合作方身份，不改变用户角色、价格、积分或分佣规则。
func (s *Store) AcceptInvitation(ctx context.Context, raw, userID string) (*Partner, error) {
	if strings.TrimSpace(raw) == "" || userID == "" {
		return nil, ErrInviteInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	inv, err := scanInvitation(tx.QueryRowContext(ctx, `SELECT `+invitationCols+`
		FROM partner_invitations WHERE token_hash=$1 FOR UPDATE`, inviteHash(raw)))
	if err != nil {
		return nil, err
	}
	var userEmail, userStatus string
	if err = tx.QueryRowContext(ctx, `SELECT email,status FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&userEmail, &userStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInviteEmail
		}
		return nil, err
	}
	if strings.ToLower(strings.TrimSpace(userEmail)) != inv.Email {
		return nil, ErrInviteEmail
	}
	if userStatus != "active" {
		return nil, ErrInviteEmail
	}
	if inv.Status == InviteAccepted {
		if inv.AcceptedUserID == userID && inv.AcceptedPartnerID != "" {
			if p, e := scanPartner(tx.QueryRowContext(ctx, `SELECT `+partnerCols+` FROM partners WHERE id=$1`, inv.AcceptedPartnerID)); e == nil {
				if e = tx.Commit(); e != nil {
					return nil, e
				}
				return p, nil
			}
		}
		return nil, ErrInviteUsed
	}
	if inv.Status == InviteRevoked {
		return nil, ErrInviteInvalid
	}
	if inv.Status == InviteExpired {
		return nil, ErrInviteExpired
	}
	// If the account was opened directly after the invite, bind the invitation to
	// that existing partner rather than creating a second identity.
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM partners WHERE user_id=$1 FOR UPDATE`, userID).Scan(&existing)
	if err == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE partner_invitations
			SET status='accepted',accepted_user_id=$2,accepted_partner_id=$3,accepted_at=now(),updated_at=now()
			WHERE id=$1 AND status='pending'`, inv.ID, userID, existing); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return s.ByID(ctx, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "partner-accept:"+userID); err != nil {
		return nil, err
	}
	// Re-check after serializing accepts for this user.
	err = tx.QueryRowContext(ctx, `SELECT id FROM partners WHERE user_id=$1 FOR UPDATE`, userID).Scan(&existing)
	if err == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE partner_invitations
			SET status='accepted',accepted_user_id=$2,accepted_partner_id=$3,accepted_at=now(),updated_at=now()
			WHERE id=$1 AND status='pending'`, inv.ID, userID, existing); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return s.ByID(ctx, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	partnerID := newID()
	var code string
	inserted := false
	for attempt := 0; attempt < 5 && !inserted; attempt++ {
		if code, err = NewCode(); err != nil {
			return nil, err
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO partners(id,user_id,name,intro,wechat,code,status)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, partnerID, userID, inv.Name, inv.Intro, inv.Wechat, code, StatusActive)
		if e != nil {
			return nil, e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return nil, e
		}
		if n == 1 {
			inserted = true
			break
		}
		// Either a concurrent accept won (advisory lock should prevent it) or the
		// random code collided. Re-read the user's partner; otherwise retry.
		if e = tx.QueryRowContext(ctx, `SELECT id FROM partners WHERE user_id=$1`, userID).Scan(&existing); e == nil {
			break
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	if !inserted {
		if existing != "" {
			if _, err = tx.ExecContext(ctx, `UPDATE partner_invitations
				SET status='accepted',accepted_user_id=$2,accepted_partner_id=$3,accepted_at=now(),updated_at=now()
				WHERE id=$1 AND status='pending'`, inv.ID, userID, existing); err != nil {
				return nil, err
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			return s.ByID(ctx, existing)
		}
		return nil, ErrCodeTaken
	}
	if err = s.audit(ctx, tx, partnerID, inv.InvitedBy, "invite_accept", map[string]any{"invitation": inv.ID, "email": inv.Email}); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE partner_invitations
		SET status='accepted',accepted_user_id=$2,accepted_partner_id=$3,accepted_at=now(),updated_at=now()
		WHERE id=$1 AND status='pending'`, inv.ID, userID, partnerID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.ByID(ctx, partnerID)
}

func (s *Store) InvitationsPage(ctx context.Context, kw, status string, page, size int) (*model.PageResp, error) {
	page, size = partnerPaging(page, size)
	kw = strings.TrimSpace(kw)
	where := ` FROM partner_invitations WHERE ($1='' OR email ILIKE $2 OR name ILIKE $2)`
	args := []any{kw, "%" + kw + "%"}
	switch status {
	case InviteExpired:
		where += ` AND status='pending' AND expires_at<=now()`
	case InvitePending:
		where += ` AND status='pending' AND expires_at>now()`
	case InviteAccepted, InviteRevoked:
		where += ` AND status=$3`
		args = append(args, status)
	case "":
	default:
		return nil, fmt.Errorf("邀请状态无效")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+invitationCols+where+`
		ORDER BY created_at DESC,id DESC LIMIT $`+fmt.Sprint(len(args)+1)+` OFFSET $`+fmt.Sprint(len(args)+2),
		append(args, size, int64(page-1)*int64(size))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Invitation{}
	for rows.Next() {
		inv, e := scanInvitation(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, *inv)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &model.PageResp{Items: items, Total: total, Page: page, PageSize: size}, nil
}
