package service

import (
	"crypto/hmac"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"drama-platform/internal/model"
	"github.com/google/uuid"
)

const (
	loginCodeTTL      = 10 * time.Minute
	loginCodeCooldown = 60 * time.Second
	loginCodeMaxTries = 5
)

func sixDigitCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// RequestLoginCode atomically reserves email/IP/global send budgets; mail is outside the transaction.
func (s *Service) RequestLoginCode(email string, ips ...string) error {
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return errors.New("请输入正确的邮箱地址")
	}
	sender := s.loginCodeSender
	if sender == nil {
		if !NewMailer().Enabled() {
			return ErrMailUnavailable
		}
		sender = s.SendLoginCode
	}
	ip := "unknown"
	if len(ips) > 0 && ips[0] != "" {
		ip = ips[0]
	}
	code, err := sixDigitCode()
	if err != nil {
		return ErrAuthUnavailable
	}
	if err := s.pruneAuthHistory(); err != nil {
		return err
	}
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	tx, err := s.db.Begin()
	if err != nil {
		return ErrAuthUnavailable
	}
	defer tx.Rollback()
	var claimed string
	err = tx.QueryRow(`INSERT INTO auth_code_sends(email,challenge_id) VALUES($1,$2)
 ON CONFLICT(email) DO UPDATE SET challenge_id=EXCLUDED.challenge_id,last_started_at=now()
 WHERE auth_code_sends.last_started_at<=now()-$3::interval RETURNING challenge_id`, email, id, loginCodeCooldown.String()).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAuthRateLimited
	}
	if err != nil {
		return ErrAuthUnavailable
	}
	for _, rate := range []struct {
		domain, key string
		limit       int
	}{{"send-global", "all", 200}, {"send-ip", ip, 20}, {"send-email", email, 5}} {
		if err := s.takeAuthRate(tx, rate.domain, rate.key, rate.limit, time.Hour); err != nil {
			return err
		}
	}
	// A single current challenge is valid; re-sending cannot reset the separate failed-attempt window.
	if _, err := tx.Exec(`UPDATE login_codes SET expires_at=LEAST(expires_at,now()) WHERE email=$1 AND used_at IS NULL`, email); err != nil {
		return ErrAuthUnavailable
	}
	if _, err := tx.Exec(`INSERT INTO login_codes(id,email,code_hash,expires_at) VALUES($1,$2,$3,now()+$4::interval)`, id, email, s.hashLoginCode(email, code), loginCodeTTL.String()); err != nil {
		return ErrAuthUnavailable
	}
	if tx.Commit() != nil {
		return ErrAuthUnavailable
	}
	if err := sender(email, code); err != nil {
		// Release only our reservation. A delayed failure cannot erase a newer challenge.
		cleanup, err := s.db.Begin()
		if err != nil {
			return ErrAuthUnavailable
		}
		defer cleanup.Rollback()
		// Match reservation -> challenge order used by RequestLoginCode.
		if _, err := cleanup.Exec(`DELETE FROM auth_code_sends WHERE email=$1 AND challenge_id=$2`, email, id); err != nil {
			return ErrAuthUnavailable
		}
		if _, err := cleanup.Exec(`DELETE FROM login_codes WHERE id=$1`, id); err != nil {
			return ErrAuthUnavailable
		}
		if cleanup.Commit() != nil {
			return ErrAuthUnavailable
		}
		return errors.New("验证码发送失败，请稍后再试")
	}
	return nil
}

// pruneAuthHistory never holds reservation/challenge/rate locks across tables.
// Each statement is its own bounded transaction and skips rows used by a live request.
func (s *Service) pruneAuthHistory() error {
	for _, query := range []string{
		`DELETE FROM login_codes WHERE id IN (
 SELECT id FROM login_codes WHERE expires_at<now()-interval '24 hours' OR used_at<now()-interval '24 hours'
 ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED)
 AND (expires_at<now()-interval '24 hours' OR used_at<now()-interval '24 hours')`,
		`DELETE FROM auth_rate_limits WHERE key IN (
 SELECT key FROM auth_rate_limits WHERE window_started_at<now()-interval '24 hours'
 ORDER BY key LIMIT 100 FOR UPDATE SKIP LOCKED)
 AND window_started_at<now()-interval '24 hours'`,
		`DELETE FROM auth_code_sends WHERE email IN (
 SELECT email FROM auth_code_sends WHERE last_started_at<now()-interval '24 hours'
 ORDER BY email LIMIT 100 FOR UPDATE SKIP LOCKED)
 AND last_started_at<now()-interval '24 hours'`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			return ErrAuthUnavailable
		}
	}
	return nil
}

func (s *Service) codeFailure(tx *sql.Tx, email, id string, result error) error {
	if _, err := tx.Exec(`UPDATE login_codes SET attempts=LEAST(attempts+1,$2) WHERE id=$1`, id, loginCodeMaxTries); err != nil {
		return ErrAuthUnavailable
	}
	if err := s.takeAuthRate(tx, "code-fail-email", email, loginCodeMaxTries, 15*time.Minute); err != nil {
		result = err
	}
	if errors.Is(result, ErrAuthUnavailable) {
		return result
	}
	return commitAuthError(tx, result)
}
func (s *Service) AuthenticateCode(email, code, totp, ip string) (*model.User, error) {
	email = normalizeEmail(email)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	defer tx.Rollback()
	if err := s.takeAuthRate(tx, "code-ip", ip, 100, 15*time.Minute); err != nil {
		return nil, commitAuthError(tx, err)
	}
	u, err := readLoginIdentity(tx, email)
	if err != nil {
		return nil, commitAuthError(tx, err)
	}
	if err := s.checkAuthRate(tx, "code-fail-email", email, loginCodeMaxTries, 15*time.Minute); err != nil {
		return nil, commitAuthError(tx, err)
	}
	var id, hash string
	var attempts int
	err = tx.QueryRow(`SELECT id,code_hash,attempts FROM login_codes
 WHERE email=$1 AND used_at IS NULL AND expires_at>now() ORDER BY created_at DESC,id DESC LIMIT 1 FOR UPDATE`, email).Scan(&id, &hash, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, commitAuthError(tx, ErrInvalidCredentials)
	}
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	if attempts >= loginCodeMaxTries {
		return nil, commitAuthError(tx, ErrAuthRateLimited)
	}
	if !hmac.Equal([]byte(s.hashLoginCode(email, code)), []byte(hash)) {
		return nil, s.codeFailure(tx, email, id, ErrInvalidCredentials)
	}
	if err := s.verifySecondFactor(tx, u, totp); err != nil {
		if errors.Is(err, ErrNeedTOTP) {
			// No credential has been consumed; persist only this request's IP allowance.
			return nil, commitAuthError(tx, err)
		}
		if errors.Is(err, ErrInvalidTOTP) {
			return nil, s.codeFailure(tx, email, id, err)
		}
		return nil, err
	}
	result, err := tx.Exec(`UPDATE login_codes SET used_at=now() WHERE id=$1 AND used_at IS NULL`, id)
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return nil, ErrAuthUnavailable
	}
	if err := s.finishLogin(tx, u, ip); err != nil {
		return nil, err
	}
	return &u.user, nil
}

// LoginUserByCode is retained for internal consumers and applies the full login policy.
func (s *Service) LoginUserByCode(email, code string, totpCodes ...string) (string, error) {
	totp := ""
	if len(totpCodes) > 0 {
		totp = totpCodes[0]
	}
	u, err := s.AuthenticateCode(email, code, totp, "internal")
	if err != nil {
		return "", err
	}
	return u.ID, nil
}
