package router

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPortableStaticPreservesBackendRoutes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("SPA"), 0600)
	t.Setenv("WEKNORA_WEB_DIR", dir)
	r := gin.New()
	serveFrontendStatic(r)
	for _, path := range []string{"/api", "/api/v1/missing", "/mcp", "/mcp/tools", "/files/a", "/health", "/r/file", "/swagger/index.html"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Body.String() == "SPA" {
			t.Errorf("swallowed backend route %s", path)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/knowledge", nil))
	if w.Body.String() != "SPA" {
		t.Fatalf("SPA missing %s", w.Body.String())
	}
}
