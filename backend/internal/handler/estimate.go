package handler

import (
	"drama-platform/internal/service"
	"github.com/gin-gonic/gin"
	"strconv"
)

func (h *Handler) EstimateProject(c *gin.Context) {
	seconds, e := strconv.Atoi(c.Query("totalSeconds"))
	if e != nil {
		fail(c, 400, "时长参数错误")
		return
	}
	estimate, e := service.EstimateProject(seconds)
	if e != nil {
		fail(c, 400, e.Error())
		return
	}
	ok(c, estimate)
}
