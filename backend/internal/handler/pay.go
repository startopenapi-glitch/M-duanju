package handler

import (
	"encoding/base64"
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	qrcode "github.com/skip2/go-qrcode"

	"drama-platform/internal/service"
)

// PayStatus 公开：告知前端支付能力
func (h *Handler) PayStatus(c *gin.Context) {
	wp := service.NewWechatPay()
	ok(c, gin.H{
		"enabled":   wp.Enabled(),
		"h5Enabled": wp.Enabled() && wp.H5Enabled,
		"mock":      wp.Enabled() && wp.Mock,
	})
}

// CreatePayOrder 创建充值订单
// 请求：{ amountYuan?, amountCents?, channel }
func (h *Handler) CreatePayOrder(c *gin.Context) {
	uid := c.GetString("userID")
	var body struct {
		AmountYuan  float64 `json:"amountYuan"`
		AmountCents int64   `json:"amountCents"`
		Channel     string  `json:"channel"`
		RunID       string  `json:"runId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	// 金额一律换算成整数分，前端绝不参与计算
	cents := body.AmountCents
	if cents <= 0 && body.AmountYuan > 0 {
		cents = int64(body.AmountYuan*100 + 0.5)
	}
	if cents <= 0 {
		fail(c, 400, "缺少金额")
		return
	}

	order, codeURL, h5URL, downlevel, err := h.svc.CreatePayOrder(
		uid, cents, body.Channel, c.ClientIP(), body.RunID)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}

	if downlevel {
		resp := gin.H{"downlevel": true, "message": "已提交人工审核，审核通过后自动到账"}
		if order != nil {
			resp["id"] = order.ID
			if order.RunID != "" {
				resp["runId"] = order.RunID
			}
		}
		ok(c, resp)
		return
	}

	resp := gin.H{
		"id":          order.ID,
		"outTradeNo":  order.OutTradeNo,
		"amountCents": order.AmountCents,
		"channel":     order.Channel,
		"status":      order.Status,
		"expiresIn":   1800,
		"downlevel":   false,
	}
	if order.RunID != "" {
		resp["runId"] = order.RunID
	}
	if codeURL != "" {
		// 二维码 PNG 直出 data URI，前端直接放 img src
		if png, err := qrcode.Encode(codeURL, qrcode.Medium, 512); err == nil {
			resp["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
		resp["codeUrl"] = codeURL
	}
	if h5URL != "" {
		resp["h5Url"] = h5URL
	}
	ok(c, resp)
}

// GetPayStatus 查单 + 落账（主动查单是核心确认路径）
func (h *Handler) GetPayStatus(c *gin.Context) {
	uid := c.GetString("userID")
	orderID := c.Param("id")

	ownerID, err := h.svc.OrderOwner(orderID)
	if err != nil {
		fail(c, 404, "订单不存在")
		return
	}
	if ownerID != uid && c.GetString("role") != "admin" {
		fail(c, 403, "无权访问该订单")
		return
	}

	state, err := h.svc.ConfirmOrderByQuery(orderID)
	if err != nil {
		log.Printf("[pay] 查单异常 %s: %v", orderID, err)
	}
	o, _ := h.svc.GetOrder(orderID)
	resp := gin.H{"id": orderID, "status": state}
	if o != nil {
		resp["amountCents"] = o.AmountCents
		resp["channel"] = o.Channel
		resp["createdAt"] = o.CreatedAt
	}
	ok(c, resp)
}

// ConfirmOrder 手动查单（前端「查询支付结果」按钮）
func (h *Handler) ConfirmOrder(c *gin.Context) {
	uid := c.GetString("userID")
	orderID := c.Param("id")

	ownerID, err := h.svc.OrderOwner(orderID)
	if err != nil {
		fail(c, 404, "订单不存在")
		return
	}
	if ownerID != uid && c.GetString("role") != "admin" {
		fail(c, 403, "无权访问该订单")
		return
	}
	state, err := h.svc.ConfirmOrderByQuery(orderID)
	if err != nil {
		fail(c, 500, "查询失败："+err.Error())
		return
	}
	ok(c, gin.H{"status": state})
}

// WechatNotify 微信支付回调
//
// 未配置 APIv3 密钥时返回 503，服务不崩溃；
// 支付确认始终由对账 worker / 主动查单兜底。
func (h *Handler) WechatNotify(c *gin.Context) {
	raw, _ := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	defer c.Request.Body.Close()

	wp := service.NewWechatPay()
	if !wp.Enabled() || len(wp.APIv3Key) != 32 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "FAIL", "message": "回调验签未配置"})
		return
	}

	tx, err := wp.VerifyAndDecryptNotify(c.Request.Header, raw)
	if err != nil {
		log.Printf("[pay] 回调校验失败: %v", err)
		h.svc.LogPayNotify("", "", "VERIFY_FAIL", err.Error(), string(raw))
		c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "verify failed"})
		return
	}

	if err := h.svc.SettleFromNotify(tx); err != nil {
		log.Printf("[pay] 回调入账失败 %s: %v", tx.OutTradeNo, err)
		h.svc.LogPayNotify(tx.OutTradeNo, tx.TransactionID, "TRANSACTION.SUCCESS", err.Error(), string(raw))
		// 返回 500 让微信重试；对账 worker 也会兜底
		c.JSON(http.StatusInternalServerError, gin.H{"code": "FAIL", "message": "settle failed"})
		return
	}

	h.svc.LogPayNotify(tx.OutTradeNo, tx.TransactionID, "TRANSACTION.SUCCESS", "ok", string(raw))
	c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "ok"})
}
