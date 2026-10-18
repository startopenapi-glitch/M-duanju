package router

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestReviewForwardedIPTrustBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := Setup(nil, "synthetic", "")
	r.GET("/review-ip", func(c *gin.Context) { c.String(200, c.ClientIP()) })
	for _, x := range []struct{ remote, xff, want string }{
		{"198.51.100.9:54321", "203.0.113.44", "198.51.100.9"},
		{"127.0.0.1:54321", "203.0.113.44, 198.51.100.9", "198.51.100.9"},
		{"[::1]:54321", "203.0.113.44, 198.51.100.9", "198.51.100.9"},
	} {
		q := httptest.NewRequest("GET", "/review-ip", nil)
		q.RemoteAddr = x.remote
		q.Header.Set("X-Forwarded-For", x.xff)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if w.Body.String() != x.want {
			t.Errorf("remote=%s got=%s want=%s", x.remote, w.Body.String(), x.want)
		}
	}
}
