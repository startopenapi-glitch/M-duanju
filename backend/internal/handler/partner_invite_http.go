package handler

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	"drama-platform/internal/partner"
)

func inviteFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, partner.ErrInviteEmail):
		c.JSON(403, gin.H{"ok": false, "code": "email_mismatch", "error": err.Error()})
	case errors.Is(err, partner.ErrInviteExpired):
		c.JSON(410, gin.H{"ok": false, "code": "invite_expired", "error": err.Error()})
	case errors.Is(err, partner.ErrInviteUsed):
		c.JSON(410, gin.H{"ok": false, "code": "invite_used", "error": err.Error()})
	case errors.Is(err, partner.ErrInviteInvalid):
		c.JSON(404, gin.H{"ok": false, "code": "invite_invalid", "error": err.Error()})
	case errors.Is(err, partner.ErrInviteMailUser), errors.Is(err, partner.ErrAlreadyExists):
		c.JSON(409, gin.H{"ok": false, "code": "already_partner", "error": err.Error()})
	case errors.Is(err, partner.ErrInviteName), errors.Is(err, partner.ErrInviteIntro), errors.Is(err, partner.ErrInviteWechat):
		c.JSON(400, gin.H{"ok": false, "error": err.Error()})
	default:
		c.JSON(400, gin.H{"ok": false, "error": err.Error()})
	}
}

// defaultInviteName derives the initial public partner name from the mailbox.
// The agent can edit it in the partner settings after accepting.
func defaultInviteName(email string) string {
	local := strings.ToLower(strings.TrimSpace(email))
	if i := strings.LastIndex(local, "@"); i > 0 {
		local = local[:i]
	}
	local = strings.TrimSpace(strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(local))
	if local == "" {
		local = "合作伙伴"
	}
	r := []rune(local)
	if len(r) > 80 {
		r = r[:80]
	}
	return strings.TrimSpace(string(r))
}

// AdminCreatePartnerInvitation 创建/重发邀请并发送邮件；邀请只需邮箱和微信。
// name/intro 仅保留为兼容 API 的可选字段，管理端不再要求填写。
func (h *Handler) AdminCreatePartnerInvitation(c *gin.Context) {
	var in struct {
		Email  string `json:"email"`
		Name   string `json:"name"`
		Intro  string `json:"intro"`
		Wechat string `json:"wechat"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Email) == "" || strings.TrimSpace(in.Wechat) == "" {
		c.JSON(400, gin.H{"error": "需要邀请邮箱和微信"})
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = defaultInviteName(in.Email)
	}
	inv, token, err := h.svc.PartnerStore().CreateInvitation(c.Request.Context(), c.GetString("userID"), in.Email, name, in.Intro, in.Wechat)
	if err != nil {
		inviteFailure(c, err)
		return
	}
	if err := h.svc.SendPartnerInvite(inv.Email, inv.Name, token); err != nil {
		c.JSON(503, gin.H{"ok": false, "code": "invite_mail_failed", "error": "邀请已创建，但邮件发送失败，请重试发送", "data": inv})
		return
	}
	ok(c, inv)
}

func (h *Handler) AdminListPartnerInvitations(c *gin.Context) {
	page, size := atoiDefault(c.Query("page"), 1), atoiDefault(c.Query("pageSize"), 20)
	items, err := h.svc.PartnerStore().InvitationsPage(c.Request.Context(), c.Query("kw"), c.Query("status"), page, size)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	ok(c, items)
}

func (h *Handler) AdminResendPartnerInvitation(c *gin.Context) {
	inv, token, err := h.svc.PartnerStore().ResendInvitation(c.Request.Context(), c.GetString("userID"), c.Param("id"))
	if err != nil {
		inviteFailure(c, err)
		return
	}
	if err := h.svc.SendPartnerInvite(inv.Email, inv.Name, token); err != nil {
		c.JSON(503, gin.H{"ok": false, "code": "invite_mail_failed", "error": "邀请链接已更新，但邮件发送失败，请重试发送", "data": inv})
		return
	}
	ok(c, inv)
}

func (h *Handler) AdminRevokePartnerInvitation(c *gin.Context) {
	inv, err := h.svc.PartnerStore().RevokeInvitation(c.Request.Context(), c.GetString("userID"), c.Param("id"))
	if err != nil {
		inviteFailure(c, err)
		return
	}
	ok(c, inv)
}

// PartnerInvitationLookup 公开只读：凭邮件中的高熵 token 查看邀请摘要；不返回其它账号数据。
func (h *Handler) PartnerInvitationLookup(c *gin.Context) {
	inv, err := h.svc.PartnerStore().LookupInvitation(c.Request.Context(), c.Query("token"))
	if err != nil {
		inviteFailure(c, err)
		return
	}
	ok(c, inv)
}

// PartnerInvitationAccept 只允许当前登录且邮箱匹配的账号接受；不改角色、不改价格/积分规则。
func (h *Handler) PartnerInvitationAccept(c *gin.Context) {
	var in struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Token) == "" {
		c.JSON(400, gin.H{"error": "需要邀请 token"})
		return
	}
	p, err := h.svc.PartnerStore().AcceptInvitation(c.Request.Context(), in.Token, c.GetString("userID"))
	if err != nil {
		inviteFailure(c, err)
		return
	}
	ok(c, p)
}
