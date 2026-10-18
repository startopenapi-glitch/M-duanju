package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"drama-platform/internal/partner"
	"drama-platform/internal/service"
)

// 代理自助接口：范围一律由当前会话确定，绝不接受客户端提交的 partner_id。
func (h *Handler) currentPartner(c *gin.Context) (*partner.Partner, bool) {
	p, err := h.svc.PartnerStore().ByUser(c.Request.Context(), c.GetString("userID"))
	if err != nil {
		if errors.Is(err, partner.ErrNotFound) {
			c.JSON(404, gin.H{"error": "未开通合作伙伴"})
			return nil, false
		}
		c.JSON(500, gin.H{"error": "查询失败"})
		return nil, false
	}
	if p.Status != partner.StatusActive {
		// 暂停后不能再读取客户数据
		c.JSON(403, gin.H{"error": "合作伙伴已暂停"})
		return nil, false
	}
	return p, true
}

func (h *Handler) PartnerMe(c *gin.Context) {
	p, e := h.svc.PartnerStore().ByUser(c.Request.Context(), c.GetString("userID"))
	if errors.Is(e, partner.ErrNotFound) {
		ok(c, nil)
		return
	}
	if e != nil {
		fail(c, 500, "身份查询失败，请重试")
		return
	}
	if p.Status != partner.StatusActive {
		ok(c, gin.H{"id": p.ID, "status": p.Status})
		return
	}
	ok(c, p)
}

func (h *Handler) PartnerOverview(c *gin.Context) {
	p, okp := h.currentPartner(c)
	if !okp {
		return
	}
	ov, err := h.svc.PartnerStore().Overview(c.Request.Context(), p.ID)
	if err != nil {
		c.JSON(500, gin.H{"error": "统计失败"})
		return
	}
	ok(c, ov)
}

func (h *Handler) PartnerCustomers(c *gin.Context) {
	p, okp := h.currentPartner(c)
	if !okp {
		return
	}
	cs, err := h.svc.PartnerStore().CustomersPage(c.Request.Context(), p.ID, atoiDefault(c.Query("page"), 1), atoiDefault(c.Query("pageSize"), 20))
	if err != nil {
		c.JSON(500, gin.H{"error": "查询失败"})
		return
	}
	ok(c, cs)
}

func (h *Handler) PartnerOrders(c *gin.Context) {
	p, okp := h.currentPartner(c)
	if !okp {
		return
	}
	rows, err := h.svc.PartnerStore().OrdersPage(c.Request.Context(), p.ID, atoiDefault(c.Query("page"), 1), atoiDefault(c.Query("pageSize"), 20))
	if err != nil {
		c.JSON(500, gin.H{"error": "查询失败"})
		return
	}
	ok(c, rows)
}

func (h *Handler) AdminListPartners(c *gin.Context) {
	items, err := h.svc.PartnerStore().ListPage(c.Request.Context(), c.Query("kw"), c.Query("status"), atoiDefault(c.Query("page"), 1), atoiDefault(c.Query("pageSize"), 20))
	if err != nil {
		c.JSON(500, gin.H{"error": "查询失败"})
		return
	}
	ok(c, items)
}

// PartnerUpdateMe 代理自助修改公开资料（名称/简介/客服微信），带版本校验。
func (h *Handler) PartnerUpdateMe(c *gin.Context) {
	p, okp := h.currentPartner(c)
	if !okp {
		return
	}
	var in struct {
		Name    string `json:"name"`
		Intro   string `json:"intro"`
		Wechat  string `json:"wechat"`
		Version int64  `json:"version"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"error": "参数错误"})
		return
	}
	np, err := h.svc.PartnerStore().UpdateProfile(c.Request.Context(), c.GetString("userID"), p.ID, in.Name, in.Intro, in.Wechat, in.Version)
	if err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	ok(c, np)
}

// StartRun 生成入口：创建（或幂等复用）一次生成执行，并给出积分判定。
// 路由必须挂在鉴权组内——否则 c.GetString("userID") 恒为空，会退化成 404/越权。
func (h *Handler) StartRun(c *gin.Context) {
	var req service.StartRunReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数错误"})
		return
	}
	res, err := h.svc.StartRun(c.GetString("userID"), c.Param("id"), &req)
	if err != nil {
		var rejected *service.RunNotCreatedError
		if errors.As(err, &rejected) {
			c.JSON(409, gin.H{"error": err.Error(), "code": "run_not_created"})
			return
		}
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	ok(c, res)
}

// GetLatestRun 返回项目最新执行状态（项目工作台轮询）。
func (h *Handler) GetLatestRun(c *gin.Context) {
	v, err := h.svc.LatestRun(c.GetString("userID"), c.Param("id"))
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	ok(c, v)
}

// CancelRun 由项目 owner 停止自动推进；已提交任务继续核对与结算。
func (h *Handler) CancelRun(c *gin.Context) {
	if err := h.svc.CancelRun(c.GetString("userID"), c.Param("id"), c.Param("rid")); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"cancelled": true})
}
