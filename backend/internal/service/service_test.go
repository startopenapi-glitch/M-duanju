package service

// 核心逻辑单测：签名、金额核验、TOTP、幂等
// 不依赖数据库的部分在这里覆盖；需要 DB 的走集成测试（见 *_db_test.go）

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// ─────────── 微信签名 ───────────

func TestBuildSignMessage(t *testing.T) {
	// GET 请求 body 为空串，但仍要有换行
	got := buildSignMessage("GET", "/v3/pay/transactions/out-trade-no/DR123?mchid=1900000109",
		"1554208460", "593BEC0C930BF1AFEB40B4A08C8FB242", "")
	want := "GET\n/v3/pay/transactions/out-trade-no/DR123?mchid=1900000109\n1554208460\n593BEC0C930BF1AFEB40B4A08C8FB242\n\n"
	if got != want {
		t.Fatalf("签名串不匹配\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildSignMessage_POSTWithBody(t *testing.T) {
	body := `{"appid":"wx123"}`
	got := buildSignMessage("POST", "/v3/pay/transactions/native", "1554208460", "NONCE", body)
	want := "POST\n/v3/pay/transactions/native\n1554208460\nNONCE\n" + body + "\n"
	if got != want {
		t.Fatalf("签名串不匹配\n got: %q\nwant: %q", got, want)
	}
	// 必须正好 5 段 + 结尾换行
	if strings.Count(got, "\n") != 5 {
		t.Fatalf("换行数量错误: %d", strings.Count(got, "\n"))
	}
}

// ─────────── 金额估算 ───────────

func TestEstimateShotCost(t *testing.T) {
	cases := []struct {
		sec  float64
		res  string
		want int64
	}{
		{5, "720P", 200},     // 5 × 40
		{5, "1080P", 400},    // 5 × 80
		{5, "standard", 100}, // 5 × 20
		{4, "standard", 80},  // 4 × 20
		{3, "standard", 80},  // 最低 80
		{2, "standard", 80},  // 40 → 最低 80
		{1, "720P", 80},      // 40 → 最低 80
		{5, "unknown", 200},  // 未知分辨率沿用 720P 估算
	}
	for _, c := range cases {
		if got := EstimateShotCost(c.sec, c.res); got != c.want {
			t.Errorf("EstimateShotCost(%v, %s) = %d, want %d", c.sec, c.res, got, c.want)
		}
	}
}

// ─────────── TOTP ───────────

func TestTOTPRoundTrip(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) < 16 {
		t.Fatalf("secret 太短: %s", secret)
	}
	code, err := totpCodeAt(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 {
		t.Fatalf("验证码应为 6 位，实际 %q", code)
	}
	if !VerifyTOTP(secret, code) {
		t.Fatal("当前时间点的验证码应当通过")
	}
}

func TestTOTPRejectsWrongCode(t *testing.T) {
	secret, _ := GenerateTOTPSecret()
	// 取当前码后改一位，必须失败
	code, _ := totpCodeAt(secret, time.Now())
	bad := code[:5] + string('0'+(code[5]-'0'+1)%10)
	if VerifyTOTP(secret, bad) {
		t.Fatal("错误验证码不应通过")
	}
	// 长度不对、空、非数字都要拒绝
	for _, s := range []string{"", "12345", "1234567", "abcdef"} {
		if VerifyTOTP(secret, s) {
			t.Fatalf("%q 不应通过", s)
		}
	}
}

func TestTOTPSkewWindow(t *testing.T) {
	// A fixed clock proves +/- one STEP; wall-clock minus 31 seconds sometimes
	// belongs to two steps earlier at the boundary and must not be accepted.
	const secret = "JBSWY3DPEHPK3PXP"
	for _, now := range []time.Time{time.Unix(1800000000, 0), time.Unix(1800000015, 0)} {
		for _, offset := range []int{-3, -2, -1, 0, 1, 2, 3} {
			code, e := totpCodeAt(secret, now.Add(time.Duration(offset)*30*time.Second))
			if e != nil {
				t.Fatal(e)
			}
			_, valid := verifiedTOTPStep(secret, code, now)
			want := offset >= -1 && offset <= 1
			if valid != want {
				t.Fatalf("offset=%d valid=%v want=%v", offset, valid, want)
			}
		}
	}
}

func TestTOTPURIContainsRequiredParams(t *testing.T) {
	uri := TOTPURI("ABC234", "a@b.com", "M-duanju")
	for _, sub := range []string{"otpauth://totp/", "secret=ABC234", "algorithm=SHA1", "digits=6", "period=30"} {
		if !strings.Contains(uri, sub) {
			t.Errorf("URI 缺少 %q: %s", sub, uri)
		}
	}
}

// ─────────── AES-GCM 回调解密 ───────────

func TestDecryptAESGCMRejectsBadKey(t *testing.T) {
	// 密钥长度不对必须报错
	if _, err := decryptAESGCM("short", "123456789012", "aad", "AAAA"); err == nil {
		t.Fatal("错误长度的密钥应当报错")
	}
	// 32 位密钥但密文非法 → 报错
	if _, err := decryptAESGCM(strings.Repeat("k", 32), "123456789012", "aad", "AAAA"); err == nil {
		t.Fatal("非法密文应当报错")
	}
}

// ─────────── 回调验签前置校验 ───────────

func TestVerifyNotifyRequiresAPIv3Key(t *testing.T) {
	w := &WechatPay{APIv3Key: ""} // 未配密钥
	h := http.Header{}
	if _, err := w.VerifyAndDecryptNotify(h, []byte(`{}`)); err == nil {
		t.Fatal("未配 APIv3 密钥时应报错（对应 503）")
	}
}

func TestVerifyNotifyRejectsOldTimestamp(t *testing.T) {
	w := &WechatPay{APIv3Key: strings.Repeat("k", 32)}
	h := http.Header{}
	h.Set("Wechatpay-Timestamp", "1000000000") // 2001 年
	if _, err := w.VerifyAndDecryptNotify(h, []byte(`{}`)); err == nil {
		t.Fatal("过期时间戳应被拒绝")
	}
}

func TestVerifyNotifyRejectsWrongSerial(t *testing.T) {
	w := &WechatPay{APIv3Key: strings.Repeat("k", 32), PlatformSerial: "EXPECTED"}
	h := http.Header{}
	h.Set("Wechatpay-Serial", "OTHER")
	h.Set("Wechatpay-Timestamp", time.Now().Format("1136239445"))
	if _, err := w.VerifyAndDecryptNotify(h, []byte(`{}`)); err == nil {
		t.Fatal("平台证书序列号不匹配应被拒绝")
	}
}

// ─────────── 恢复码 ───────────

func TestGenerateRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 8 {
		t.Fatalf("应生成 8 个恢复码，实际 %d", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 10 {
			t.Errorf("恢复码应为 10 位: %q", c)
		}
		if seen[c] {
			t.Errorf("恢复码重复: %q", c)
		}
		seen[c] = true
	}
}

func TestHashOneIsStable(t *testing.T) {
	if hashOne("abc") != hashOne("ABC") {
		t.Fatal("大小写应归一化后哈希一致")
	}
	if hashOne("abc") == hashOne("abd") {
		t.Fatal("不同输入不应产生相同哈希")
	}
}
