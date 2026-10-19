package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"drama-platform/internal/model"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrAuthUnavailable    = errors.New("登录服务暂不可用，请稍后再试")
	ErrInvalidCredentials = errors.New("登录凭证错误或已失效")
	ErrNeedTOTP           = errors.New("该账号已启用双因素认证，请输入验证器验证码或恢复码")
	ErrInvalidTOTP        = errors.New("双因素验证码或恢复码错误")
	ErrAuthRateLimited    = errors.New("请求过于频繁，请稍后再试")
	ErrMailUnavailable    = errors.New("邮件服务未配置，请联系管理员")
)

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func (s *Service) authDigest(domain, value string) string {
	derive := hmac.New(sha256.New, []byte(s.jwtSecret))
	derive.Write([]byte("drama-platform/auth/" + domain + "/v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Service) hashLoginCode(email, code string) string {
	return "v1:" + s.authDigest("login-code", normalizeEmail(email)+"\x00"+strings.TrimSpace(code))
}

// takeAuthRate must run inside a short transaction. UPSERT serializes the window.
func (s *Service) takeAuthRate(tx *sql.Tx, domain, identity string, limit int, window time.Duration) error {
	var attempts int
	err := tx.QueryRow(`INSERT INTO auth_rate_limits(key,attempts) VALUES($1,1)
 ON CONFLICT(key) DO UPDATE SET
 attempts=CASE WHEN auth_rate_limits.window_started_at<=now()-$2::interval THEN 1 ELSE LEAST(auth_rate_limits.attempts+1,$3+1) END,
 window_started_at=CASE WHEN auth_rate_limits.window_started_at<=now()-$2::interval THEN now() ELSE auth_rate_limits.window_started_at END
 RETURNING attempts`, s.authDigest("rate-limit", domain+":"+identity), window.String(), limit).Scan(&attempts)
	if err != nil {
		return ErrAuthUnavailable
	}
	if attempts > limit {
		return ErrAuthRateLimited
	}
	return nil
}
func (s *Service) checkAuthRate(tx *sql.Tx, domain, identity string, limit int, window time.Duration) error {
	var exhausted bool
	err := tx.QueryRow(`SELECT attempts >= $2 AND window_started_at > now()-$3::interval FROM auth_rate_limits WHERE key=$1`, s.authDigest("rate-limit", domain+":"+identity), limit, window.String()).Scan(&exhausted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return ErrAuthUnavailable
	}
	if exhausted {
		return ErrAuthRateLimited
	}
	return nil
}
func commitAuthError(tx *sql.Tx, result error) error {
	if tx.Commit() != nil {
		return ErrAuthUnavailable
	}
	return result
}

type loginIdentity struct {
	user                   model.User
	hash, secret, recovery string
	enabled                bool
	locked                 sql.NullTime
	lastStep               int64
}

func readLoginIdentity(tx *sql.Tx, email string) (*loginIdentity, error) {
	// Two case variants must never be resolved by guessing an account.
	rows, err := tx.Query(`SELECT id,email,password_hash,COALESCE(nickname,''),role,credits,status,created_at,updated_at,session_version,
 COALESCE(totp_secret,''),COALESCE(recovery_codes,''),totp_enabled,locked_until,totp_last_step
 FROM users WHERE lower(btrim(email))=$1 ORDER BY id FOR UPDATE`, email)
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	defer rows.Close()
	var found *loginIdentity
	for rows.Next() {
		if found != nil {
			return nil, ErrInvalidCredentials
		}
		u := &loginIdentity{}
		if rows.Scan(&u.user.ID, &u.user.Email, &u.hash, &u.user.Nickname, &u.user.Role, &u.user.Credits, &u.user.Status, &u.user.CreatedAt, &u.user.UpdatedAt, &u.user.SessionVersion, &u.secret, &u.recovery, &u.enabled, &u.locked, &u.lastStep) != nil {
			return nil, ErrAuthUnavailable
		}
		found = u
	}
	if rows.Err() != nil {
		return nil, ErrAuthUnavailable
	}
	if found == nil {
		return nil, ErrInvalidCredentials
	}
	if found.user.Status != "active" {
		return nil, ErrInvalidCredentials
	}
	if found.locked.Valid && found.locked.Time.After(time.Now()) {
		return nil, ErrAuthRateLimited
	}
	return found, nil
}

func (s *Service) verifySecondFactor(tx *sql.Tx, u *loginIdentity, code string) error {
	if !u.enabled {
		return nil
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ErrNeedTOTP
	}
	if len(code) == totpDigits {
		now := time.Now()
		for i := -totpSkew; i <= totpSkew; i++ {
			at := now.Add(time.Duration(i*totpPeriod) * time.Second)
			step := at.Unix() / totpPeriod
			want, err := totpCodeAt(u.secret, at)
			if err == nil && step > u.lastStep && hmac.Equal([]byte(want), []byte(code)) {
				if _, err := tx.Exec(`UPDATE users SET totp_last_step=$1 WHERE id=$2`, step, u.user.ID); err != nil {
					return ErrAuthUnavailable
				}
				return nil
			}
		}
	}
	if len(code) == 10 && u.recovery != "" {
		var list []string
		if json.Unmarshal([]byte(u.recovery), &list) == nil {
			h := hashOne(code)
			for i, stored := range list {
				if hmac.Equal([]byte(h), []byte(stored)) {
					list = append(list[:i], list[i+1:]...)
					b, _ := json.Marshal(list)
					if _, err := tx.Exec(`UPDATE users SET recovery_codes=$1,updated_at=now() WHERE id=$2`, string(b), u.user.ID); err != nil {
						return ErrAuthUnavailable
					}
					if _, err := tx.Exec(`INSERT INTO audit_logs(user_id,action) VALUES($1,'use_recovery_code')`, u.user.ID); err != nil {
						return ErrAuthUnavailable
					}
					return nil
				}
			}
		}
	}
	return ErrInvalidTOTP
}
func (s *Service) finishLogin(tx *sql.Tx, u *loginIdentity, ip string) error {
	if _, err := tx.Exec(`UPDATE users SET failed_login_count=0,locked_until=NULL,last_login_at=now(),updated_at=now() WHERE id=$1`, u.user.ID); err != nil {
		return ErrAuthUnavailable
	}
	if _, err := tx.Exec(`INSERT INTO audit_logs(user_id,action,ip) VALUES($1,'login',$2)`, u.user.ID, ip); err != nil {
		return ErrAuthUnavailable
	}
	if err := tx.Commit(); err != nil {
		return ErrAuthUnavailable
	}
	return nil
}
func (s *Service) passwordFailure(tx *sql.Tx, u *loginIdentity, result error) error {
	if _, err := tx.Exec(`UPDATE users SET failed_login_count=failed_login_count+1,
 locked_until=CASE WHEN failed_login_count+1 >= $2 THEN now()+$3::interval ELSE locked_until END WHERE id=$1`, u.user.ID, maxFailedLogin, lockDuration.String()); err != nil {
		return ErrAuthUnavailable
	}
	return commitAuthError(tx, result)
}

// AuthenticatePassword applies the same account and second-factor policy as email login.
func (s *Service) AuthenticatePassword(email, password, totp, ip string) (*model.User, error) {
	email = normalizeEmail(email)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	defer tx.Rollback()
	if err := s.takeAuthRate(tx, "password-ip", ip, 100, 15*time.Minute); err != nil {
		return nil, commitAuthError(tx, err)
	}
	u, err := readLoginIdentity(tx, email)
	if err != nil {
		return nil, commitAuthError(tx, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.hash), []byte(password)) != nil {
		return nil, s.passwordFailure(tx, u, ErrInvalidCredentials)
	}
	if err := s.verifySecondFactor(tx, u, totp); err != nil {
		if errors.Is(err, ErrNeedTOTP) {
			// No credential has been consumed; persist only this request's IP allowance.
			return nil, commitAuthError(tx, err)
		}
		if errors.Is(err, ErrInvalidTOTP) {
			return nil, s.passwordFailure(tx, u, err)
		}
		return nil, err
	}
	if err := s.finishLogin(tx, u, ip); err != nil {
		return nil, err
	}
	return &u.user, nil
}

func (s *Service) CurrentSession(userID string, version int64) (string, error) {
	var role string
	if version < 1 {
		return "", ErrInvalidCredentials
	}
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id=$1 AND status='active' AND session_version=$2`, userID, version).Scan(&role); err != nil {
		return "", ErrInvalidCredentials
	}
	return role, nil
}
