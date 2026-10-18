package service

// 微信支付 APIv3 客户端
//
// 设计原则（照生产规格）：
//  1. 支付确认 100% 依赖「主动查单」，回调只作为加速信号
//  2. 金额一律 int64 分，全程无浮点
//  3. 手写签名，不引支付 SDK
//  4. HTTP 客户端：10s 超时 / 禁跟随重定向 / 响应体上限 1MB / 不跳过 TLS
//  5. 下单类请求失败绝不重试（防重复下单）

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const wxpayBase = "https://api.mch.weixin.qq.com"

// WechatPay 支付客户端
type WechatPay struct {
	AppID          string
	MchID          string
	SerialNo       string // 商户证书序列号
	APIv3Key       string // 32 位，用于回调解密
	PlatformSerial string // 平台证书序列号
	PlatformPubKey *rsa.PublicKey
	NotifyURL      string
	H5Enabled      bool
	Mock           bool
	PrivateKey     *rsa.PrivateKey
	HTTP           *http.Client
}

// NewWechatPay 从环境变量装配；关键配置缺失返回 nil（表示未启用）
func NewWechatPay() *WechatPay {
	appid := strings.TrimSpace(os.Getenv("WXPAY_APPID"))
	mchid := strings.TrimSpace(os.Getenv("WXPAY_MCHID"))
	serial := strings.TrimSpace(os.Getenv("WXPAY_SERIAL"))
	apiv3 := ReadSecret("WXPAY_APIV3_KEY")
	keyPath := strings.TrimSpace(os.Getenv("WXPAY_KEY_PATH"))
	mock := os.Getenv("WXPAY_MOCK") == "1" && os.Getenv("NODE_ENV") != "production"

	if appid == "" || mchid == "" || serial == "" || keyPath == "" {
		if !mock {
			return nil
		}
	}

	w := &WechatPay{
		AppID:          appid,
		MchID:          mchid,
		SerialNo:       serial,
		APIv3Key:       apiv3,
		PlatformSerial: strings.TrimSpace(os.Getenv("WXPAY_PLATFORM_SERIAL")),
		H5Enabled:      os.Getenv("WXPAY_H5_ENABLED") == "1",
		Mock:           mock,
		NotifyURL:      strings.TrimSpace(os.Getenv("WXPAY_NOTIFY_URL")),
		// ★ HTTP 硬约束：10s 超时 + 禁止重定向
		HTTP: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("禁止跟随重定向")
			},
		},
	}
	if w.NotifyURL == "" {
		w.NotifyURL = getenvDefault("PUBLIC_BASE_URL", "http://localhost:8088") + "/api/pay/notify"
	}

	if keyPath != "" {
		if key, err := loadPrivateKeyFile(keyPath); err == nil {
			w.PrivateKey = key
		} else if !mock {
			return nil
		}
	}
	if p := strings.TrimSpace(os.Getenv("WXPAY_PLATFORM_PUBKEY_PATH")); p != "" {
		if pub, err := loadPublicKeyFile(p); err == nil {
			w.PlatformPubKey = pub
		}
	}

	if (w.MchID == "" || w.PrivateKey == nil) && !mock {
		return nil
	}
	return w
}

// Enabled 是否已启用
func (w *WechatPay) Enabled() bool { return w != nil }

// ReadSecret 优先读 <KEY>_FILE 指向的文件（秘密不进环境变量），退回直接读环境变量
// 导出给诊断等模块复用，避免各处读法不一致导致误判
func ReadSecret(key string) string {
	if p := os.Getenv(key + "_FILE"); p != "" {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return strings.TrimSpace(string(b))
		}
	}
	return strings.TrimSpace(os.Getenv(key))
}

// ─────────── 密钥加载 ───────────

func loadPrivateKeyFile(path string) (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return loadPrivateKey(string(raw))
}

func loadPrivateKey(pemData string) (*rsa.PrivateKey, error) {
	blk, _ := pem.Decode([]byte(pemData))
	if blk == nil {
		return nil, errors.New("PEM decode failed")
	}
	if key, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
		return key, nil
	}
	if key8, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
		if rk, ok := key8.(*rsa.PrivateKey); ok {
			return rk, nil
		}
	}
	return nil, errors.New("unsupported key format")
}

func loadPublicKeyFile(path string) (*rsa.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(raw)
	if blk == nil {
		return nil, errors.New("PEM decode failed")
	}
	if pub, err := x509.ParsePKIXPublicKey(blk.Bytes); err == nil {
		if rk, ok := pub.(*rsa.PublicKey); ok {
			return rk, nil
		}
	}
	if cert, err := x509.ParseCertificate(blk.Bytes); err == nil {
		if rk, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return rk, nil
		}
	}
	return nil, errors.New("unsupported public key format")
}

// ─────────── 签名 ───────────

func buildSignMessage(method, pathWithQuery, timestamp, nonce, body string) string {
	return method + "\n" + pathWithQuery + "\n" + timestamp + "\n" + nonce + "\n" + body + "\n"
}

func signMessage(msg string, privateKey *rsa.PrivateKey) (string, error) {
	sum := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func randomNonce(n int) string {
	b := make([]byte, (n+1)/2)
	rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func (w *WechatPay) do(method, pathWithQuery string, payload interface{}) ([]byte, error) {
	if w == nil {
		return nil, errors.New("微信支付未配置")
	}
	var bodyStr string
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		bodyStr = string(b)
	}

	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce := randomNonce(32)
	sig, err := signMessage(buildSignMessage(method, pathWithQuery, ts, nonce, bodyStr), w.PrivateKey)
	if err != nil {
		return nil, err
	}
	auth := fmt.Sprintf(
		`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		w.MchID, nonce, ts, w.SerialNo, sig)

	var reader io.Reader
	if bodyStr != "" {
		reader = bytes.NewReader([]byte(bodyStr))
	}
	req, err := http.NewRequest(method, wxpayBase+pathWithQuery, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "drama-platform/1.0")

	resp, err := w.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// ★ 响应体上限 1MB
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 300 {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		json.Unmarshal(raw, &e)
		m := e.Message
		if m == "" {
			m = truncate(string(raw), 180)
		}
		return nil, fmt.Errorf("微信支付错误(%d/%s): %s", resp.StatusCode, e.Code, m)
	}
	return raw, nil
}

// ─────────── 1. Native 下单 ───────────

// CreateNativeOrder 扫码支付。outTradeNo ≤32 位，仅 [A-Za-z0-9_-]
// ★ 失败绝不重试
func (w *WechatPay) CreateNativeOrder(outTradeNo, description string, amountCents int64, expireAt time.Time) (string, error) {
	if w.Mock {
		return "weixin://wxpay/bizpayurl?pr=MOCK" + outTradeNo, nil
	}
	payload := map[string]interface{}{
		"appid":        w.AppID,
		"mchid":        w.MchID,
		"description":  description,
		"out_trade_no": outTradeNo,
		"notify_url":   w.NotifyURL,
		"amount":       map[string]interface{}{"total": amountCents, "currency": "CNY"},
	}
	if !expireAt.IsZero() {
		payload["time_expire"] = expireAt.Format("2006-01-02T15:04:05-07:00")
	}
	raw, err := w.do("POST", "/v3/pay/transactions/native", payload)
	if err != nil {
		return "", err
	}
	var r struct {
		CodeURL string `json:"code_url"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	if r.CodeURL == "" {
		return "", errors.New("微信未返回 code_url")
	}
	return r.CodeURL, nil
}

// ─────────── 2. H5 下单 ───────────

func (w *WechatPay) CreateH5Order(outTradeNo, description string, amountCents int64, clientIP, appURL string) (string, error) {
	if w.Mock {
		return "https://wx.tenpay.com/cgi-bin/mmpayweb-bin/checkmweb?prepay_id=MOCK", nil
	}
	payload := map[string]interface{}{
		"appid":        w.AppID,
		"mchid":        w.MchID,
		"description":  description,
		"out_trade_no": outTradeNo,
		"notify_url":   w.NotifyURL,
		"amount":       map[string]interface{}{"total": amountCents, "currency": "CNY"},
		"scene_info": map[string]interface{}{
			"payer_client_ip": clientIP,
			"h5_info": map[string]string{
				"type": "Wap", "app_name": "短剧工厂", "app_url": appURL,
			},
		},
	}
	raw, err := w.do("POST", "/v3/pay/transactions/h5", payload)
	if err != nil {
		return "", err
	}
	var r struct {
		H5URL string `json:"h5_url"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	// ★ 防开放重定向：只允许微信官方域名
	if !strings.HasPrefix(r.H5URL, "https://wx.tenpay.com/") {
		return "", errors.New("h5_url 非微信官方域名，已拒绝")
	}
	return r.H5URL, nil
}

// ─────────── 3. 查单（核心确认路径） ───────────

// QueryOrderResp 查单响应
type QueryOrderResp struct {
	AppID          string `json:"appid"`
	MchID          string `json:"mchid"`
	OutTradeNo     string `json:"out_trade_no"`
	TransactionID  string `json:"transaction_id"`
	TradeState     string `json:"trade_state"`
	TradeStateDesc string `json:"trade_state_desc"`
	SuccessTime    string `json:"success_time"`
	Amount         struct {
		Total    int64  `json:"total"`
		Currency string `json:"currency"`
	} `json:"amount"`
}

// QueryOrder 主动查单
func (w *WechatPay) QueryOrder(outTradeNo string) (*QueryOrderResp, error) {
	if w.Mock {
		return &QueryOrderResp{
			AppID: w.AppID, MchID: w.MchID, OutTradeNo: outTradeNo,
			TradeState: "NOTPAY", TradeStateDesc: "mock 未支付",
		}, nil
	}
	path := "/v3/pay/transactions/out-trade-no/" + outTradeNo + "?mchid=" + w.MchID
	raw, err := w.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var r QueryOrderResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ─────────── 4. 关单 ───────────

// CloseOrder 关闭订单
func (w *WechatPay) CloseOrder(outTradeNo string) error {
	if w.Mock {
		return nil
	}
	path := "/v3/pay/transactions/out-trade-no/" + outTradeNo + "/close"
	_, err := w.do("POST", path, map[string]string{"mchid": w.MchID})
	return err
}

// ─────────── 回调验签（6 层） ───────────

// NotifyResource 回调加密资源
type NotifyResource struct {
	Algorithm      string `json:"algorithm"`
	Ciphertext     string `json:"ciphertext"`
	AssociatedData string `json:"associated_data"`
	Nonce          string `json:"nonce"`
	OriginalType   string `json:"original_type"`
}

// NotifyBody 回调体
type NotifyBody struct {
	ID           string         `json:"id"`
	EventType    string         `json:"event_type"`
	ResourceType string         `json:"resource_type"`
	Resource     NotifyResource `json:"resource"`
}

// DecryptedTransaction 解密后的交易信息
type DecryptedTransaction struct {
	AppID         string `json:"appid"`
	MchID         string `json:"mchid"`
	OutTradeNo    string `json:"out_trade_no"`
	TransactionID string `json:"transaction_id"`
	TradeState    string `json:"trade_state"`
	SuccessTime   string `json:"success_time"`
	Amount        struct {
		Total    int64  `json:"total"`
		Currency string `json:"currency"`
	} `json:"amount"`
}

// VerifyAndDecryptNotify 6 层校验 + AES-GCM 解密
func (w *WechatPay) VerifyAndDecryptNotify(header http.Header, body []byte) (*DecryptedTransaction, error) {
	if w == nil {
		return nil, errors.New("微信支付未配置")
	}
	if len(w.APIv3Key) != 32 {
		return nil, errors.New("回调验签未配置（缺 32 位 APIv3 密钥）")
	}

	// ① 平台证书序列号
	if w.PlatformSerial != "" {
		if s := header.Get("Wechatpay-Serial"); s != w.PlatformSerial {
			return nil, fmt.Errorf("平台证书序列号不匹配: %s", s)
		}
	}

	// ② 时间戳 ±300s
	tsStr := header.Get("Wechatpay-Timestamp")
	var ts int64
	fmt.Sscanf(tsStr, "%d", &ts)
	if ts == 0 || absInt64(time.Now().Unix()-ts) > 300 {
		return nil, errors.New("回调时间戳超出 ±300 秒")
	}

	// ③ 签名验证
	if w.PlatformPubKey != nil {
		nonce := header.Get("Wechatpay-Nonce")
		msg := tsStr + "\n" + nonce + "\n" + string(body) + "\n"
		sum := sha256.Sum256([]byte(msg))
		sig, err := base64.StdEncoding.DecodeString(header.Get("Wechatpay-Signature"))
		if err != nil {
			return nil, errors.New("回调签名解码失败")
		}
		if err := rsa.VerifyPKCS1v15(w.PlatformPubKey, crypto.SHA256, sum[:], sig); err != nil {
			return nil, errors.New("回调验签失败（可能伪造）")
		}
	}

	var nb NotifyBody
	if err := json.Unmarshal(body, &nb); err != nil {
		return nil, errors.New("回调 body 解析失败")
	}

	// ④ 事件类型
	if nb.EventType != "TRANSACTION.SUCCESS" {
		return nil, fmt.Errorf("忽略非成功事件: %s", nb.EventType)
	}

	// ⑤ 加密算法
	if nb.Resource.Algorithm != "AEAD_AES_256_GCM" {
		return nil, fmt.Errorf("不支持的加密算法: %s", nb.Resource.Algorithm)
	}

	// ⑥ 解密
	plain, err := decryptAESGCM(w.APIv3Key, nb.Resource.Nonce, nb.Resource.AssociatedData, nb.Resource.Ciphertext)
	if err != nil {
		return nil, err
	}
	var tx DecryptedTransaction
	if err := json.Unmarshal(plain, &tx); err != nil {
		return nil, err
	}
	return &tx, nil
}

func decryptAESGCM(apiV3Key, nonce, aad, ciphertextB64 string) ([]byte, error) {
	if len(apiV3Key) != 32 {
		return nil, errors.New("APIv3 key 必须 32 字节")
	}
	ct, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(apiV3Key))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != 12 {
		return nil, errors.New("nonce 必须 12 字节")
	}
	return gcm.Open(nil, []byte(nonce), ct, []byte(aad))
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
