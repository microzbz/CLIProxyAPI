package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

type stubAuthRateLimitStore struct {
	value config.AuthRateLimit
}

func (s *stubAuthRateLimitStore) LoadAuthRateLimit(context.Context) (config.AuthRateLimit, error) {
	return s.value, nil
}

func (s *stubAuthRateLimitStore) SaveAuthRateLimit(_ context.Context, value config.AuthRateLimit) error {
	s.value = value
	return nil
}

func TestAuthRateLimitEndpointsUseRuntimeStore(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	store := &stubAuthRateLimitStore{
		value: config.AuthRateLimit{
			Limit:           5,
			WindowSeconds:   60,
			CooldownSeconds: 90,
			PerAuthRules:    []string{"user@example.com|limit|3"},
		},
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	h.authRateLimitStore = store

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-rate-limit", nil)
	h.GetAuthRateLimit(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got config.AuthRateLimit
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Limit != 5 || got.WindowSeconds != 60 || got.CooldownSeconds != 90 || len(got.PerAuthRules) != 1 {
		t.Fatalf("unexpected auth rate limit payload: %#v", got)
	}

	rec = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(
		http.MethodPut,
		"/v0/management/auth-rate-limit",
		bytes.NewBufferString(`{"limit":7,"window-seconds":30,"cooldown-seconds":45,"per-auth-rules":["user@example.com|limit|2"]}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	h.PutAuthRateLimit(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if store.value.Limit != 7 || store.value.WindowSeconds != 30 || store.value.CooldownSeconds != 45 {
		t.Fatalf("stored auth rate limit = %#v, want updated values", store.value)
	}
	if len(store.value.PerAuthRules) != 1 || store.value.PerAuthRules[0] != "user@example.com|limit|2" {
		t.Fatalf("stored auth rate limit rules = %#v", store.value.PerAuthRules)
	}
	if h.cfg.AuthRateLimit.Limit != 7 || h.cfg.AuthRateLimit.WindowSeconds != 30 || h.cfg.AuthRateLimit.CooldownSeconds != 45 {
		t.Fatalf("handler cfg auth rate limit = %#v, want updated values", h.cfg.AuthRateLimit)
	}
}
