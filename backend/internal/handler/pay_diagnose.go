package handler

// 支付配置自检：返回每项配置的就绪状态，便于运维排查
// GET /api/admin/pay/diagnose

import (
	"os"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"drama-platform/internal/service"
)

type checkItem struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Value  string `json:"value,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

func (h *Handler) AdminPayDiagnose(c *gin.Context) {
	items := []checkItem{}

	appid := os.Getenv("WXPAY_APPID")
	mchid := os.Getenv("WXPAY_MCHID")
	serial := os.Getenv("WXPAY_SERIAL")
	keyPath := os.Getenv("WXPAY_KEY_PATH")
	// ★ 必须与 wechatpay.go 用同一套读取逻辑（支持 *_FILE），否则诊断会误报
	apiv3 := service.ReadSecret("WXPAY_APIV3_KEY")
	notify := os.Getenv("WXPAY_NOTIFY_URL")

	// APPID: wx + 16 位十六进制
	okAppID := regexp.MustCompile(`^wx[0-9a-f]{16}$`).MatchString(appid)
	items = append(items, checkItem{
		Name: "WXPAY_APPID", OK: okAppID, Value: mask(appid, 6),
		Hint: pickStr(okAppID, "", "格式应为 wx + 16 位小写十六进制"),
	})

	// MCHID: 10 位数字
	okMch := regexp.MustCompile(`^\d{10}$`).MatchString(mchid)
	items = append(items, checkItem{
		Name: "WXPAY_MCHID", OK: okMch, Value: mchid,
		Hint: pickStr(okMch, "", "应为 10 位数字商户号"),
	})

	// SERIAL: 40 位大写十六进制（SHA1 指纹）
	okSerial := regexp.MustCompile(`^[0-9A-F]{40}$`).MatchString(serial)
	items = append(items, checkItem{
		Name: "WXPAY_SERIAL", OK: okSerial, Value: mask(serial, 8),
		Hint: pickStr(okSerial, "", "应为 40 位大写十六进制证书序列号"),
	})

	// 私钥文件：存在、可读、能解析成 RSA
	keyOK, keyMsg := checkPrivateKey(keyPath)
	items = append(items, checkItem{
		Name: "商户私钥 apiclient_key.pem", OK: keyOK,
		Value: keyPath,
		Hint:  keyMsg,
	})

	// APIv3 密钥：可空（留空则回调验签关闭，走主动查单）
	v3OK := len(apiv3) == 32
	items = append(items, checkItem{
		Name: "WXPAY_APIV3_KEY", OK: v3OK,
		Value: pickStr(v3OK, mask(apiv3, 4), "（未配置）"),
		Hint:  pickStr(v3OK, "已配置 32 位，回调可验签", "未配置：回调验签关闭，支付确认走主动查单（仍可正常收款）"),
	})

	// 回调地址
	okNotify := strings.HasPrefix(notify, "https://")
	items = append(items, checkItem{
		Name: "WXPAY_NOTIFY_URL", OK: okNotify, Value: notify,
		Hint: pickStr(okNotify, "", "必须为 https"),
	})

	// 整体状态
	wp := service.NewWechatPay()
	ready := wp != nil && wp.Enabled()

	// 缺失项汇总
	var missing []string
	for _, it := range items {
		if !it.OK {
			missing = append(missing, it.Name)
		}
	}

	ok(c, gin.H{
		"enabled":  ready,
		"items":    items,
		"missing":  missing,
		"mode":     pickStr(ready, "查单模式（回调仅加速）", "未启用，充值走人工审核"),
		"note":     pickStr(v3OK, "建议补上 APIv3 密钥以启用回调验签；不补也能正常收款", ""),
	})
}

func mask(s string, keep int) string {
	if len(s) <= keep {
		return s
	}
	return s[:keep] + strings.Repeat("*", len(s)-keep)
}

func pickStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
