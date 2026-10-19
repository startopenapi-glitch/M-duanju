package handler

import (
	"database/sql"
	"drama-platform/internal/service"
	"errors"
	"github.com/gin-gonic/gin"
	"strings"
)

func adminResult(c *gin.Context, v interface{}, err error) {
	if err == nil {
		ok(c, v)
		return
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		fail(c, 404, "记录不存在")
	case errors.Is(err, service.ErrAdminConflict), errors.Is(err, service.ErrRefundConflict), errors.Is(err, service.ErrRefundLegacy):
		fail(c, 409, err.Error())
	case errors.Is(err, service.ErrAdminForbidden):
		fail(c, 403, err.Error())
	case errors.Is(err, service.ErrAdminInvalid):
		fail(c, 400, err.Error())
	case errors.Is(err, service.ErrRefundUnavailable):
		fail(c, 503, "退款通道不可用，未提交退款")
	default:
		fail(c, 500, "操作未完成，请刷新状态后重试")
	}
}
func (h *Handler) AdminOverview(c *gin.Context) { v, e := h.svc.AdminOverview(); adminResult(c, v, e) }
func (h *Handler) AdminOrderDetail(c *gin.Context) {
	v, e := h.svc.AdminOrderDetail(c.Param("id"))
	adminResult(c, v, e)
}
func (h *Handler) AdminJobDetail(c *gin.Context) {
	v, e := h.svc.AdminJobDetail(c.Param("id"))
	adminResult(c, v, e)
}
func (h *Handler) AdminUserDetail(c *gin.Context) {
	v, e := h.svc.AdminUserDetail(c.Param("id"))
	adminResult(c, v, e)
}
func (h *Handler) AdminJobsList(c *gin.Context) {
	v, e := h.svc.AdminJobsList(c.Query("kw"), c.Query("status"), atoiDefault(c.Query("page"), 1), atoiDefault(c.Query("pageSize"), 20))
	adminResult(c, v, e)
}
func (h *Handler) AdminCredits(c *gin.Context) {
	v, e := h.svc.AdminCredits(c.Query("kw"), atoiDefault(c.Query("page"), 1), atoiDefault(c.Query("pageSize"), 20))
	adminResult(c, v, e)
}
func (h *Handler) AdminAudit(c *gin.Context) {
	v, e := h.svc.AdminAudit(c.Query("kw"), atoiDefault(c.Query("page"), 1), atoiDefault(c.Query("pageSize"), 20))
	adminResult(c, v, e)
}
func (h *Handler) AdminSystem(c *gin.Context) { v, e := h.svc.AdminSystem(); adminResult(c, v, e) }
func (h *Handler) AdminUserStatus(c *gin.Context) {
	var in struct {
		Status  string `json:"status"`
		Version int64  `json:"version"`
		Reason  string `json:"reason" binding:"required,max=255"`
	}
	if e := c.ShouldBindJSON(&in); e != nil {
		fail(c, 400, "请填写状态和原因")
		return
	}
	v, e := h.svc.SetUserStatus(c.GetString("userID"), c.Param("id"), in.Status, in.Reason, in.Version)
	adminResult(c, v, e)
}
func (h *Handler) AdminQueryRefund(c *gin.Context) {
	v, e := h.svc.QueryRefund(c.Param("id"), c.GetString("userID"))
	adminResult(c, v, e)
}
func (h *Handler) AdminConfirmOrder(c *gin.Context) {
	// Query a real channel only. A missing config/mock mode must not look verified.
	order, e := h.svc.GetOrder(c.Param("id"))
	if e != nil {
		adminResult(c, nil, e)
		return
	}
	wp := service.NewWechatPay()
	if order.Mock || strings.TrimSpace(order.OutTradeNo) == "" || wp == nil || wp.Mock {
		fail(c, 409, "此订单无法通过真实支付渠道核验")
		return
	}
	status, e := h.svc.ConfirmOrderByQuery(order.ID)
	adminResult(c, gin.H{"status": status}, e)
}
