package handler

import (
	"encoding/base64"
	"github.com/skip2/go-qrcode"
	"net/http"

	"github.com/gin-gonic/gin"

	"drama-platform/internal/model"
)

// ─────────── 修改密码 ───────────

func (h *Handler) ChangePassword(c *gin.Context) {
	uid := c.GetString("userID")
	var req model.ChangePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "新密码需 8-64 位")
		return
	}
	if req.OldPassword == req.NewPassword {
		fail(c, 400, "新密码不能与旧密码相同")
		return
	}
	if err := h.svc.ChangePassword(uid, req.OldPassword, req.NewPassword); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"changed": true})
}

// ─────────── TOTP ───────────

// TOTPSetup 生成绑定二维码
func (h *Handler) TOTPSetup(c *gin.Context) {
	uid := c.GetString("userID")
	secret, uri, err := h.svc.SetupTOTP(uid)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	png, err := qrcode.Encode(uri, qrcode.Medium, 200)
	if err != nil {
		fail(c, 503, "二维码生成失败，请稍后再试")
		return
	}
	c.Header("Cache-Control", "no-store")
	ok(c, gin.H{"secret": secret, "uri": uri, "qrUrl": uri, "qrDataUrl": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)})
}

// TOTPEnable 校验 6 位码后启用，返回恢复码（只显示一次）
func (h *Handler) TOTPEnable(c *gin.Context) {
	uid := c.GetString("userID")
	var req model.TOTPVerifyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "请输入 6 位验证码")
		return
	}
	codes, err := h.svc.EnableTOTP(uid, req.Code)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{
		"enabled":       true,
		"recoveryCodes": codes,
		"message":       "请妥善保存恢复码，每码只能用一次，丢失将无法登录",
	})
}

// TOTPDisable 停用（需密码）
func (h *Handler) TOTPDisable(c *gin.Context) {
	uid := c.GetString("userID")
	var req model.TOTPDisableReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "请输入密码")
		return
	}
	if err := h.svc.DisableTOTP(uid, req.Password); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"enabled": false})
}

// TOTPStatus 查询双因素状态
func (h *Handler) TOTPStatus(c *gin.Context) {
	uid := c.GetString("userID")
	ok(c, gin.H{"enabled": h.svc.NeedTOTP(uid)})
}

// ─────────── 安全概览 ───────────

func (h *Handler) SecurityOverview(c *gin.Context) {
	uid := c.GetString("userID")
	ok(c, gin.H{
		"totpEnabled": h.svc.NeedTOTP(uid),
		"tip":         "建议为管理员账号开启双因素认证",
	})
}

var _ = http.StatusOK
