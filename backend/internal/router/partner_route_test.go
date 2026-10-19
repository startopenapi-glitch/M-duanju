package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"drama-platform/internal/handler"
	"drama-platform/internal/service"
)

// 回归护栏：代理自助接口必须挂在鉴权组内。
// 曾经因为挂到未鉴权分组，5 个端点全部 404，而所有库层测试仍然全绿。
func TestPartnerRoutesRequireAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := service.New(nil, "synthetic-test-secret")
	r := Setup(handler.New(svc, "synthetic-test-secret"), "synthetic-test-secret", "")

	paths := []string{
		"/api/partner/me",
		"/api/partner/overview",
		"/api/partner/customers",
		"/api/partner/orders",
	}
	for _, p := range paths {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, p, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s 匿名访问返回 %d，应为 401（说明该路由没有挂鉴权）", p, w.Code)
		}
	}
	// PATCH 自助改资料同样必须鉴权
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPatch, "/api/partner/me", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("PATCH /api/partner/me 匿名访问返回 %d，应为 401", w.Code)
	}
}
