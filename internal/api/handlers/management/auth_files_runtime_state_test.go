package management

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	codexauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v6/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestBuildAuthFromFileData_PreservesExistingRuntimeState(t *testing.T) {
	authDir := t.TempDir()
	path := filepath.Join(authDir, "codex.json")

	manager := coreauth.NewManager(nil, nil, nil)
	createdAt := time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)
	lastRefresh := createdAt.Add(-1 * time.Hour)
	nextRefresh := createdAt.Add(2 * time.Hour)
	nextRetry := createdAt.Add(30 * time.Minute)

	existing := &coreauth.Auth{
		ID:               "codex.json",
		FileName:         "codex.json",
		Provider:         "codex",
		Prefix:           "team-a",
		Label:            "existing@example.com",
		Status:           coreauth.StatusError,
		StatusMessage:    "quota exhausted",
		Disabled:         true,
		Unavailable:      true,
		ProxyURL:         "http://127.0.0.1:8080",
		Attributes:       map[string]string{"path": path, "source": path, "priority": "7"},
		Metadata:         map[string]any{"type": "codex", "email": "existing@example.com"},
		Quota:            coreauth.QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: nextRetry, BackoffLevel: 2},
		LastError:        &coreauth.Error{Message: "usage_limit_reached", HTTPStatus: 429},
		CreatedAt:        createdAt,
		LastRefreshedAt:  lastRefresh,
		NextRefreshAfter: nextRefresh,
		NextRetryAfter:   nextRetry,
		ModelStates: map[string]*coreauth.ModelState{
			"gpt-5.4": {
				Status:         coreauth.StatusError,
				StatusMessage:  "quota exhausted",
				Unavailable:    true,
				NextRetryAfter: nextRetry,
				LastError:      &coreauth.Error{Message: "usage_limit_reached", HTTPStatus: 429},
				Quota:          coreauth.QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: nextRetry},
			},
		},
		Runtime: "runtime-state",
	}
	if _, err := manager.Register(context.Background(), existing); err != nil {
		t.Fatalf("register existing auth: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	rebuilt, err := h.buildAuthFromFileData(path, []byte(`{"type":"codex","email":"new@example.com"}`))
	if err != nil {
		t.Fatalf("buildAuthFromFileData() error = %v", err)
	}

	if rebuilt.CreatedAt != createdAt {
		t.Fatalf("rebuilt.CreatedAt = %v, want %v", rebuilt.CreatedAt, createdAt)
	}
	if rebuilt.LastRefreshedAt != lastRefresh {
		t.Fatalf("rebuilt.LastRefreshedAt = %v, want %v", rebuilt.LastRefreshedAt, lastRefresh)
	}
	if rebuilt.NextRefreshAfter != nextRefresh {
		t.Fatalf("rebuilt.NextRefreshAfter = %v, want %v", rebuilt.NextRefreshAfter, nextRefresh)
	}
	if rebuilt.NextRetryAfter != nextRetry {
		t.Fatalf("rebuilt.NextRetryAfter = %v, want %v", rebuilt.NextRetryAfter, nextRetry)
	}
	if rebuilt.Status != coreauth.StatusError || rebuilt.StatusMessage != "quota exhausted" {
		t.Fatalf("rebuilt status = %s / %q, want error / quota exhausted", rebuilt.Status, rebuilt.StatusMessage)
	}
	if !rebuilt.Disabled || !rebuilt.Unavailable {
		t.Fatalf("rebuilt flags = disabled:%v unavailable:%v, want both true", rebuilt.Disabled, rebuilt.Unavailable)
	}
	if rebuilt.Prefix != "team-a" || rebuilt.ProxyURL != "http://127.0.0.1:8080" {
		t.Fatalf("rebuilt prefix/proxy = %q / %q, want preserved runtime values", rebuilt.Prefix, rebuilt.ProxyURL)
	}
	if rebuilt.Runtime != "runtime-state" {
		t.Fatalf("rebuilt.Runtime = %#v, want preserved runtime", rebuilt.Runtime)
	}
	if rebuilt.Quota.BackoffLevel != 2 || !rebuilt.Quota.Exceeded {
		t.Fatalf("rebuilt.Quota = %#v, want preserved quota", rebuilt.Quota)
	}
	if rebuilt.LastError == nil || rebuilt.LastError.HTTPStatus != 429 {
		t.Fatalf("rebuilt.LastError = %#v, want preserved error", rebuilt.LastError)
	}
	if rebuilt.Attributes["path"] != path || rebuilt.Attributes["source"] != path {
		t.Fatalf("rebuilt path attributes = %#v, want current path/source", rebuilt.Attributes)
	}
	if rebuilt.Attributes["priority"] != "7" {
		t.Fatalf("rebuilt.Attributes[priority] = %q, want %q", rebuilt.Attributes["priority"], "7")
	}
	state := rebuilt.ModelStates["gpt-5.4"]
	if state == nil || state.Status != coreauth.StatusError || !state.Unavailable {
		t.Fatalf("rebuilt model state = %#v, want preserved error state", state)
	}
}

func TestBuildAuthFromFileData_HydratesPriorityFromMetadata(t *testing.T) {
	authDir := t.TempDir()
	path := filepath.Join(authDir, "codex.json")

	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	rebuilt, err := h.buildAuthFromFileData(path, []byte(`{"type":"codex","email":"priority@example.com","priority":9}`))
	if err != nil {
		t.Fatalf("buildAuthFromFileData() error = %v", err)
	}
	if got := rebuilt.Attributes["priority"]; got != "9" {
		t.Fatalf("rebuilt.Attributes[priority] = %q, want %q", got, "9")
	}
	if got := rebuilt.Attributes["auth_kind"]; got != "oauth" {
		t.Fatalf("rebuilt.Attributes[auth_kind] = %q, want %q", got, "oauth")
	}
}

func TestSaveTokenRecord_SyncsRuntimeManagerWithSavedTokenMetadata(t *testing.T) {
	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.tokenStore = sdkAuth.NewFileTokenStore()

	record := &coreauth.Auth{
		ID:       "codex-sync.json",
		Provider: "codex",
		FileName: "codex-sync.json",
		Storage: &codexauth.CodexTokenStorage{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			Email:        "sync@example.com",
			Type:         "codex",
		},
		Metadata: map[string]any{
			"email":    "sync@example.com",
			"priority": 11,
		},
	}

	if _, err := h.saveTokenRecord(context.Background(), record); err != nil {
		t.Fatalf("saveTokenRecord() error = %v", err)
	}

	got, ok := manager.GetByID("codex-sync.json")
	if !ok {
		t.Fatal("expected saved auth to be registered in runtime manager")
	}
	if got.Metadata["access_token"] != "access-token" {
		t.Fatalf("runtime access_token = %#v, want %q", got.Metadata["access_token"], "access-token")
	}
	if got.Attributes["priority"] != "11" {
		t.Fatalf("runtime priority = %q, want %q", got.Attributes["priority"], "11")
	}
	if got.Attributes["auth_kind"] != "oauth" {
		t.Fatalf("runtime auth_kind = %q, want %q", got.Attributes["auth_kind"], "oauth")
	}
}
