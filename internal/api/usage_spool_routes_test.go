package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api/handlers/management"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

// The usage spool is served on both management generations behind the same
// key check: v8 under its observability group, v0 for existing consumers.
func TestUsageSpoolRoutesOnBothManagementGenerations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\nremote-management: {secret-key: test-password}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	h := management.NewHandler(cfg, path, nil)
	h.SetLocalPassword("test-password")
	s := &Server{cfg: cfg, engine: gin.New(), mgmt: h}
	s.managementRoutesEnabled.Store(true)
	s.registerManagementRoutes()

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/v0/management/usage-spool"},
		{http.MethodPost, "/v0/management/usage-spool/ack"},
		{http.MethodGet, "/v8/management/observability/usage/spool"},
		{http.MethodPost, "/v8/management/observability/usage/spool/ack"},
	} {
		for _, authorized := range []bool{false, true} {
			req := httptest.NewRequest(route.method, route.path, nil)
			req.RemoteAddr = "127.0.0.1:1234"
			want := http.StatusUnauthorized
			if authorized {
				req.Header.Set("Authorization", "Bearer test-password")
				// No spool is open in this process, so the handler itself answers.
				want = http.StatusServiceUnavailable
			}
			recorder := httptest.NewRecorder()
			s.engine.ServeHTTP(recorder, req)
			if recorder.Code != want {
				t.Fatalf("%s %s authorized=%v: status=%d want=%d body=%s", route.method, route.path, authorized, recorder.Code, want, recorder.Body.String())
			}
		}
	}
}
