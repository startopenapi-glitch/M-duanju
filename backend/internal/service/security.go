package service

// 账号安全：改密码、TOTP 绑定、登录失败锁定

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	maxFailedLogin = 5
	lockDuration   = 15 * time.Minute
	minPasswordLen = 8
)

// ChangePassword 改密码（需旧密码）
func (s *Service) ChangePassword(userID, oldPwd, newPwd string) error {
	if len(newPwd) < minPasswordLen {
		return fmt.Errorf("新密码至少 %d 位", minPasswordLen)
	}
	if oldPwd == newPwd {
		return errors.New("新密码不能与旧密码相同")
	}

	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id=$1`, userID).Scan(&hash); err != nil {
		return errors.New("用户不存在")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(oldPwd)) != nil {
		return errors.New("原密码错误")
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(newPwd), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE users SET password_hash=$1, password_changed_at=now(), updated_at=now() WHERE id=$2`,
		string(newHash), userID)
	if err == nil {
		s.Log(userID, "", "", "change_password", "", "", "")
	}
	return err
}

// ─────────── 登录锁定 ───────────

// MarkLoginFailed 记录失败次数，超限锁定
func (s *Service) MarkLoginFailed(email string) {
	s.db.Exec(`UPDATE users SET failed_login_count=failed_login_count+1,
 locked_until=CASE WHEN failed_login_count+1 >= $2 THEN now()+$3::interval ELSE locked_until END,
 updated_at=now() WHERE lower(btrim(email))=$1`, normalizeEmail(email), maxFailedLogin, lockDuration.String())
}

// ClearLoginFailed 登录成功清零
func (s *Service) ClearLoginFailed(userID string) {
	s.db.Exec(`UPDATE users SET failed_login_count=0, locked_until=NULL, last_login_at=now(), updated_at=now() WHERE id=$1`, userID)
}

// IsLocked 检查是否被锁
func (s *Service) IsLocked(email string) (bool, time.Duration) {
	var lockedUntil *time.Time
	if err := s.db.QueryRow(`SELECT locked_until FROM users WHERE lower(btrim(email))=$1`, normalizeEmail(email)).Scan(&lockedUntil); err != nil {
		return false, 0
	}
	if lockedUntil == nil || lockedUntil.Before(time.Now()) {
		return false, 0
	}
	return true, time.Until(*lockedUntil)
}

// ─────────── TOTP ───────────

// SetupTOTP 生成待确认 secret + 二维码 URI（此时还未启用）
func (s *Service) SetupTOTP(userID string) (secret, otpauth string, err error) {
	secret, err = GenerateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	var email string
	if err := s.db.QueryRow(`UPDATE users SET totp_pending_secret=$1,updated_at=now()
 WHERE id=$2 AND totp_enabled=false RETURNING email`, secret, userID).Scan(&email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", errors.New("用户不存在或已启用双因素，请先停用")
		}
		return "", "", ErrAuthUnavailable
	}
	return secret, TOTPURI(secret, email, getenvDefault("APP_NAME", "M-duanju")), nil
}

// EnableTOTP 校验 6 位码后正式启用，返回一次性恢复码
func (s *Service) EnableTOTP(userID, code string) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	defer tx.Rollback()
	var pending sql.NullString
	var enabled bool
	if err := tx.QueryRow(`SELECT totp_pending_secret,totp_enabled FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&pending, &enabled); err != nil {
		return nil, ErrAuthUnavailable
	}
	if enabled || !pending.Valid || pending.String == "" {
		return nil, errors.New("请先获取绑定二维码")
	}
	step, valid := verifiedTOTPStep(pending.String, code, time.Now())
	if !valid {
		return nil, ErrInvalidTOTP
	}
	codes, err := GenerateRecoveryCodes(8)
	if err != nil {
		return nil, err
	}
	hashed, _ := json.Marshal(hashCodes(codes))
	if _, err := tx.Exec(`UPDATE users SET totp_secret=$1,totp_enabled=true,totp_pending_secret=NULL,
 recovery_codes=$2,totp_last_step=$3,updated_at=now() WHERE id=$4`, pending.String, string(hashed), step, userID); err != nil {
		return nil, ErrAuthUnavailable
	}
	if _, err := tx.Exec(`INSERT INTO audit_logs(user_id,action) VALUES($1,'enable_totp')`, userID); err != nil {
		return nil, ErrAuthUnavailable
	}
	if tx.Commit() != nil {
		return nil, ErrAuthUnavailable
	}
	return codes, nil
}

// DisableTOTP 停用双因素（需密码）
func (s *Service) DisableTOTP(userID, password string) error {
	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id=$1`, userID).Scan(&hash); err != nil {
		return errors.New("用户不存在")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return errors.New("密码错误")
	}
	_, err := s.db.Exec(`
		UPDATE users SET totp_enabled=false, totp_secret=NULL, totp_pending_secret=NULL,
		                 recovery_codes=NULL, updated_at=now()
		WHERE id=$1`, userID)
	if err == nil {
		s.Log(userID, "", "", "disable_totp", "", "", "")
	}
	return err
}

// VerifyUserTOTP 登录时校验（支持恢复码）
func (s *Service) VerifyUserTOTP(userID, code string) bool {
	tx, err := s.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	u := &loginIdentity{}
	var status string
	if err := tx.QueryRow(`SELECT id,COALESCE(totp_secret,''),COALESCE(recovery_codes,''),totp_enabled,totp_last_step,status
 FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&u.user.ID, &u.secret, &u.recovery, &u.enabled, &u.lastStep, &status); err != nil || !u.enabled || status != "active" {
		return false
	}
	if err := s.verifySecondFactor(tx, u, code); err != nil {
		return false
	}
	return tx.Commit() == nil
}

// NeedTOTP 该用户是否需要二次验证
func (s *Service) NeedTOTP(userID string) bool {
	var enabled bool
	s.db.QueryRow(`SELECT totp_enabled FROM users WHERE id=$1`, userID).Scan(&enabled)
	return enabled
}

// ─────────── 辅助 ───────────

func hashOne(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

func hashCodes(codes []string) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = hashOne(c)
	}
	return out
}
