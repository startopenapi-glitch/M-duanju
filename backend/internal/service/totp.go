package service

// TOTP 双因素认证（RFC 6238）
// 只用 Go 标准库：crypto/hmac + sha1 + base32

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	totpDigits = 6
	totpPeriod = 30 // 秒
	totpSkew   = 1  // 允许前后各 1 个周期（±30s，容忍时钟漂移）
)

var b32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret 生成 20 字节随机 secret（base32，无填充）
func GenerateTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32NoPad.EncodeToString(b), nil
}

// TOTPURI 生成 otpauth:// URI，供前端转二维码
func TOTPURI(secret, email, issuer string) string {
	if issuer == "" {
		issuer = "M-duanju"
	}
	label := url.PathEscape(issuer + ":" + email)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// totpCodeAt 计算某个时间点的 6 位码
func totpCodeAt(secret string, t time.Time) (string, error) {
	key, err := b32NoPad.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("secret 不是合法 base32: %w", err)
	}
	counter := uint64(t.Unix() / totpPeriod)

	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, code%mod), nil
}

// VerifyTOTP 校验 6 位码，允许 ±totpSkew 个周期
func VerifyTOTP(secret, code string) bool {
	_, ok := verifiedTOTPStep(secret, code, time.Now())
	return ok
}

// verifiedTOTPStep returns the actual matching step, including tolerated clock skew.
func verifiedTOTPStep(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	for i := -totpSkew; i <= totpSkew; i++ {
		at := now.Add(time.Duration(i*totpPeriod) * time.Second)
		want, err := totpCodeAt(secret, at)
		if err != nil {
			return 0, false
		}
		if hmac.Equal([]byte(want), []byte(code)) {
			return at.Unix() / totpPeriod, true
		}
	}
	return 0, false
}

// GenerateRecoveryCodes 生成 n 个一次性恢复码（10 位，易读字符集）
func GenerateRecoveryCodes(n int) ([]string, error) {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		buf := make([]byte, 10)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		var sb strings.Builder
		for _, b := range buf {
			sb.WriteByte(charset[int(b)%len(charset)])
		}
		codes = append(codes, sb.String())
	}
	return codes, nil
}
