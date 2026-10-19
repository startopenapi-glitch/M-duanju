package handler

import (
	"github.com/gin-gonic/gin"
)

// OwnProject 校验当前用户是否有权访问 :id 对应的项目
// 放在 /projects/:id 路由组上，一处生效，避免每个 handler 各写一遍漏掉
func (h *Handler) OwnProject() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			fail(c, 400, "缺少项目 id")
			return
		}
		ownerID, err := h.svc.ProjectOwner(id)
		if err != nil {
			fail(c, 404, "项目不存在")
			return
		}
		role := c.GetString("role")
		if role != "admin" && ownerID != c.GetString("userID") {
			fail(c, 403, "无权访问该项目")
			return
		}
		c.Set("projectID", id)
		c.Set("projectOwnerID", ownerID)
		c.Next()
	}
}
