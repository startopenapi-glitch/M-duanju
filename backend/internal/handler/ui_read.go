package handler

import (
	"drama-platform/internal/service"
	"errors"
	"github.com/gin-gonic/gin"
)

func uiReadError(c *gin.Context, e error) {
	switch {
	case errors.Is(e, service.ErrUIReadInvalid):
		fail(c, 400, e.Error())
	case errors.Is(e, service.ErrUIReadNotFound):
		fail(c, 404, e.Error())
	default:
		fail(c, 500, "暂时无法核对记录，请重试")
	}
}
func (h *Handler) UIRunContext(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	v, e := h.svc.RunReadContext(c.Request.Context(), c.GetString("userID"), service.RunContextQuery{RunID: c.Query("runId"), ProjectID: c.Query("projectId"), RequestKey: c.Query("requestKey")})
	if e != nil {
		uiReadError(c, e)
		return
	}
	ok(c, v)
}
func (h *Handler) UIOrderProgress(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	v, e := h.svc.OrderProgress(c.Request.Context(), c.GetString("userID"), c.Param("id"))
	if e != nil {
		uiReadError(c, e)
		return
	}
	ok(c, v)
}
