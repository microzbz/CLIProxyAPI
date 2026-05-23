package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestPatchAuthFileFields_UpdatesAccessToken(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	store := &authoritativeListAuthStore{}
	auth := &coreauth.Auth{
		ID:       "codex-access.json",
		FileName: "codex-access.json",
		Provider: "codex",
		Status:   coreauth.StatusDisabled,
		Disabled: true,
		Attributes: map[string]string{
			"path": filepath.Join(authDir, "codex-access.json"),
		},
		Metadata: map[string]any{
			"type":         "codex",
			"access_token": "old-access-token",
		},
	}
	if _, err := store.Save(context.Background(), auth); err != nil {
		t.Fatalf("save auth: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.tokenStore = store

	body := bytes.NewBufferString(`{"name":"codex-access.json","access_token":"new-access-token"}`)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/fields", body)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PatchAuthFileFields(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	runtimeAuth, ok := manager.GetByID("codex-access.json")
	if !ok {
		t.Fatal("expected patched auth to be registered in runtime manager")
	}
	if got := runtimeAuth.Metadata["access_token"]; got != "new-access-token" {
		t.Fatalf("runtime access_token = %#v, want new-access-token", got)
	}
	stored, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list store: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored auth count = %d, want 1", len(stored))
	}
	if got := stored[0].Metadata["access_token"]; got != "new-access-token" {
		raw, _ := json.Marshal(stored[0].Metadata)
		t.Fatalf("stored access_token = %#v, want new-access-token; metadata=%s", got, raw)
	}
}
