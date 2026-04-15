package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
