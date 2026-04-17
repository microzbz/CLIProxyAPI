package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestListAuthFiles_FiltersByEnabledState(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	writeAuthFile := func(name string) string {
		t.Helper()
		path := filepath.Join(authDir, name)
		if err := os.WriteFile(path, []byte(`{"type":"codex"}`), 0o600); err != nil {
			t.Fatalf("write auth file %s: %v", name, err)
		}
		return path
	}

	manager := coreauth.NewManager(nil, nil, nil)
	register := func(auth *coreauth.Auth) {
		t.Helper()
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth %s: %v", auth.ID, err)
		}
	}

	register(&coreauth.Auth{
		ID:       "enabled-auth",
		FileName: "enabled.json",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": writeAuthFile("enabled.json"),
		},
		Metadata: map[string]any{"type": "codex"},
	})
	register(&coreauth.Auth{
		ID:       "disabled-auth",
		FileName: "disabled.json",
		Provider: "codex",
		Status:   coreauth.StatusDisabled,
		Disabled: true,
		Attributes: map[string]string{
			"path": writeAuthFile("disabled.json"),
		},
		Metadata: map[string]any{"type": "codex"},
	})

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	testCases := []struct {
		name      string
		query     string
		wantNames []string
		wantCode  int
	}{
		{name: "all", query: "/v0/management/auth-files", wantCode: http.StatusOK, wantNames: []string{"disabled.json", "enabled.json"}},
		{name: "enabled only", query: "/v0/management/auth-files?enabled=true", wantCode: http.StatusOK, wantNames: []string{"enabled.json"}},
		{name: "disabled only", query: "/v0/management/auth-files?enabled=false", wantCode: http.StatusOK, wantNames: []string{"disabled.json"}},
		{name: "invalid filter", query: "/v0/management/auth-files?enabled=maybe", wantCode: http.StatusBadRequest},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			ctx.Request = httptest.NewRequest(http.MethodGet, tc.query, nil)

			h.ListAuthFiles(ctx)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode != http.StatusOK {
				return
			}

			var payload struct {
				Files []map[string]any `json:"files"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(payload.Files) != len(tc.wantNames) {
				t.Fatalf("files len = %d, want %d; body=%s", len(payload.Files), len(tc.wantNames), rec.Body.String())
			}
			got := make([]string, 0, len(payload.Files))
			for _, file := range payload.Files {
				if name, _ := file["name"].(string); name != "" {
					got = append(got, name)
				}
			}
			for i := range tc.wantNames {
				if got[i] != tc.wantNames[i] {
					t.Fatalf("file[%d] = %q, want %q", i, got[i], tc.wantNames[i])
				}
			}
		})
	}
}

func TestListAuthFiles_PaginatesResults(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	register := func(name string) {
		t.Helper()
		path := filepath.Join(authDir, name)
		if err := os.WriteFile(path, []byte(`{"type":"codex"}`), 0o600); err != nil {
			t.Fatalf("write auth file %s: %v", name, err)
		}
		if _, err := manager.Register(context.Background(), &coreauth.Auth{
			ID:       name,
			FileName: name,
			Provider: "codex",
			Status:   coreauth.StatusActive,
			Attributes: map[string]string{
				"path": path,
			},
			Metadata: map[string]any{"type": "codex"},
		}); err != nil {
			t.Fatalf("register auth %s: %v", name, err)
		}
	}
	register("a.json")
	register("b.json")
	register("c.json")

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files?page=2&page_size=1", nil)

	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload struct {
		Files      []map[string]any `json:"files"`
		Pagination map[string]any   `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Files) != 1 {
		t.Fatalf("files len = %d, want 1; body=%s", len(payload.Files), rec.Body.String())
	}
	if got, _ := payload.Files[0]["name"].(string); got != "b.json" {
		t.Fatalf("file name = %q, want %q", got, "b.json")
	}
	if got := int(payload.Pagination["page"].(float64)); got != 2 {
		t.Fatalf("pagination.page = %d, want 2", got)
	}
	if got := int(payload.Pagination["page_size"].(float64)); got != 1 {
		t.Fatalf("pagination.page_size = %d, want 1", got)
	}
	if got := int(payload.Pagination["total"].(float64)); got != 3 {
		t.Fatalf("pagination.total = %d, want 3", got)
	}
	if got := int(payload.Pagination["total_pages"].(float64)); got != 3 {
		t.Fatalf("pagination.total_pages = %d, want 3", got)
	}
}

func TestListAuthFiles_IncludesRateLimitState(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	path := filepath.Join(authDir, "cooldown.json")
	if err := os.WriteFile(path, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	next := time.Now().Add(2 * time.Minute)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "cooldown-auth",
		FileName: "cooldown.json",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": path,
		},
		LocalRateLimit: coreauth.LocalRateLimitState{
			CooldownUntil: next,
		},
		Metadata: map[string]any{"type": "codex"},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)

	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Files) != 1 {
		t.Fatalf("files len = %d, want 1; body=%s", len(payload.Files), rec.Body.String())
	}
	if got, _ := payload.Files[0]["rate_limit_status"].(string); got != "cooldown" {
		t.Fatalf("rate_limit_status = %q, want %q", got, "cooldown")
	}
	if got, _ := payload.Files[0]["rate_limited"].(bool); !got {
		t.Fatalf("rate_limited = %v, want true", got)
	}
	if got, ok := payload.Files[0]["rate_limit_scope"].(string); !ok || got != "local-rate-limit" {
		t.Fatalf("rate_limit_scope = %v, want %q", payload.Files[0]["rate_limit_scope"], "local-rate-limit")
	}
	if got, ok := payload.Files[0]["rate_limit_retry_after"].(string); !ok || strings.TrimSpace(got) == "" {
		t.Fatalf("rate_limit_retry_after = %v, want non-empty RFC3339 string", payload.Files[0]["rate_limit_retry_after"])
	}
}

func TestListAuthFiles_FiltersByProviderAndCooldownState(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	next := time.Now().Add(10 * time.Minute)

	register := func(auth *coreauth.Auth) {
		t.Helper()
		path := filepath.Join(authDir, auth.FileName)
		if err := os.WriteFile(path, []byte(`{"type":"`+auth.Provider+`"}`), 0o600); err != nil {
			t.Fatalf("write auth file %s: %v", auth.FileName, err)
		}
		if auth.Attributes == nil {
			auth.Attributes = map[string]string{}
		}
		auth.Attributes["path"] = path
		if auth.Metadata == nil {
			auth.Metadata = map[string]any{"type": auth.Provider}
		}
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth %s: %v", auth.ID, err)
		}
	}

	register(&coreauth.Auth{
		ID:             "codex-cooldown",
		FileName:       "codex-cooldown.json",
		Provider:       "codex",
		Status:         coreauth.StatusError,
		Unavailable:    true,
		NextRetryAfter: next,
	})
	register(&coreauth.Auth{
		ID:       "codex-active",
		FileName: "codex-active.json",
		Provider: "codex",
		Status:   coreauth.StatusActive,
	})
	register(&coreauth.Auth{
		ID:             "claude-cooldown",
		FileName:       "claude-cooldown.json",
		Provider:       "claude",
		Status:         coreauth.StatusError,
		Unavailable:    true,
		NextRetryAfter: next,
	})

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files?provider=codex&state=cooldown", nil)

	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Files) != 1 {
		t.Fatalf("files len = %d, want 1; body=%s", len(payload.Files), rec.Body.String())
	}
	if got, _ := payload.Files[0]["name"].(string); got != "codex-cooldown.json" {
		t.Fatalf("file name = %q, want %q", got, "codex-cooldown.json")
	}
}
