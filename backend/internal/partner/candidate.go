package partner

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// 来源候选使用带用途隔离的 HMAC 签名，避免被当作普通 URL 参数伪造。
// 用途前缀确保同一密钥下的其它签名（如会话）不能拿来当归属证据。
const candidatePurpose = "partner_candidate.v1"

var (
	ErrCandidateInvalid = errors.New("推广来源无效")
	ErrCandidateExpired = errors.New("推广来源已过期")
)

// SignCandidate 生成 cookie 值：v1.<expUnix>.<code>.<sig>
func SignCandidate(secret, code string, now time.Time, ttl time.Duration) string {
	exp := now.Add(ttl).Unix()
	sig := candidateSig(secret, exp, code)
	return "v1." + strconv.FormatInt(exp, 10) + "." + code + "." + sig
}

// VerifyCandidate 校验签名与有效期，并返回推广码。
func VerifyCandidate(secret, raw string, now time.Time) (string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", ErrCandidateInvalid
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", ErrCandidateInvalid
	}
	code := parts[2]
	if code == "" {
		return "", ErrCandidateInvalid
	}
	// 常数时间比较，避免签名被逐字节试探
	if !hmac.Equal([]byte(parts[3]), []byte(candidateSig(secret, exp, code))) {
		return "", ErrCandidateInvalid
	}
	if now.Unix() >= exp {
		return "", ErrCandidateExpired
	}
	return code, nil
}

func candidateSig(secret string, exp int64, code string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(candidatePurpose + "|" + strconv.FormatInt(exp, 10) + "|" + code))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
