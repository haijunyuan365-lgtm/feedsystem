package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewPprofMux(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	rr := httptest.NewRecorder()

	NewPprofMux().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", rr.Code)
	}
}

// enabled=false 时，不应该启动 pprof 服务应该返回 nil, nil
func TestNewPprofServerWithDisabled(t *testing.T) {
	t.Parallel()

	pprofServer, err := NewPprofServer("api", false, "localhost:6060")
	if err != nil {
		t.Fatalf("Failed to create pprof server: %v", err)
	}
	if pprofServer != nil {
		t.Fatalf("Expected nil pprof server when disabled, got non-nil")
	}
}

// 这个测试验证的就是：即使 pprof 没启动，Close 也安全
func TestPprofServerCloseWithDisabledServer(t *testing.T) {
	t.Parallel()

	pprofServer, err := NewPprofServer("api", false, "localhost:6060")
	if err != nil {
		t.Fatalf("Failed to create pprof server: %v", err)
	}
	if err := pprofServer.Close(); err != nil {
		t.Fatalf("Expected no error when closing disabled pprof server, got: %v", err)
	}
}
