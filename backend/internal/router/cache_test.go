package router

import (
	"drama-platform/internal/handler"
	"drama-platform/internal/service"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticAndAPIResponseCacheContract(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets"), 0700)
	for name, body := range map[string]string{"index.html": "<html>synthetic SPA</html>", "assets/index-abcdef12.js": "console.log('synthetic')", "assets/logo.svg": "<svg></svg>", "favicon.ico": "synthetic-icon"} {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0600)
	}
	h := handler.New(service.New(nil, "synthetic"), "synthetic")
	r := Setup(h, "synthetic", dir)
	for _, tc := range []struct {
		path        string
		status      int
		cache, body string
	}{{"/project/test", 200, "no-cache", "synthetic SPA"}, {"/assets/index-abcdef12.js", 200, "immutable", "console.log"}, {"/assets/logo.svg", 200, "no-cache", "<svg>"}, {"/favicon.ico", 200, "no-cache", "synthetic-icon"}, {"/api/projects", 401, "no-store", ""}, {"/api/nope", 404, "no-store", ""}, {"/assets/missing-abcdef12.js", 404, "no-cache", ""}} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.status || !strings.Contains(w.Header().Get("Cache-Control"), tc.cache) || !strings.Contains(w.Body.String(), tc.body) {
				t.Fatalf("status=%d cache=%q body=%q", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
			}
		})
	}
}
