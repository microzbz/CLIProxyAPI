package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type refreshTestStore struct {
	mu    sync.Mutex
	items map[string]*coreauth.Auth
	last  *coreauth.Auth
}

func newRefreshTestStore() *refreshTestStore {
	return &refreshTestStore{items: make(map[string]*coreauth.Auth)}
}

func (s *refreshTestStore) List(context.Context) ([]*coreauth.Auth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*coreauth.Auth, 0, len(s.items))
	for _, item := range s.items {
		out = append(out, item.Clone())
	}
	return out, nil
}

func (s *refreshTestStore) Save(_ context.Context, auth *coreauth.Auth) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := auth.Clone()
	s.items[auth.ID] = clone
	s.last = clone.Clone()
	return auth.ID, nil
}

func (s *refreshTestStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return nil
}

func (s *refreshTestStore) Last() *coreauth.Auth {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return nil
	}
	return s.last.Clone()
}

type refreshTestExecutor struct{}

func (refreshTestExecutor) Identifier() string { return "codex" }

func (refreshTestExecutor) Execute(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (refreshTestExecutor) ExecuteStream(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, nil
}

func (refreshTestExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	auth.Metadata["access_token"] = "new-access-token"
	auth.Metadata["refresh_token"] = "new-refresh-token"
	auth.Metadata["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	return auth, nil
}

func (refreshTestExecutor) CountTokens(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (refreshTestExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestRefreshAuthFiles_RefreshesSelectedRTAndPersistsState(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	store := newRefreshTestStore()
	manager := coreauth.NewManager(store, nil, nil)
	manager.RegisterExecutor(refreshTestExecutor{})

	refreshable := &coreauth.Auth{
		ID:            "refreshable",
		FileName:      "refreshable.json",
		Provider:      "codex",
		Status:        coreauth.StatusDisabled,
		StatusMessage: "disabled after upstream 401",
		Disabled:      true,
		Unavailable:   true,
		LastError:     &coreauth.Error{HTTPStatus: http.StatusUnauthorized, Message: "unauthorized"},
		Attributes: map[string]string{
			"path": filepath.Join(authDir, "refreshable.json"),
		},
		Metadata: map[string]any{
			"type":          "codex",
			"refresh_token": "old-refresh-token",
			"access_token":  "old-access-token",
		},
	}
	noRT := &coreauth.Auth{
		ID:       "no-rt",
		FileName: "no-rt.json",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": filepath.Join(authDir, "no-rt.json"),
		},
		Metadata: map[string]any{"type": "codex"},
	}
	if _, err := manager.Register(context.Background(), refreshable); err != nil {
		t.Fatalf("register refreshable: %v", err)
	}
	if _, err := manager.Register(context.Background(), noRT); err != nil {
		t.Fatalf("register no-rt: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v0/management/auth-files/refresh",
		bytes.NewBufferString(`{"names":["refreshable.json","no-rt.json"]}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.RefreshAuthFiles(ctx)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusMultiStatus, rec.Body.String())
	}
	var payload struct {
		Refreshed int      `json:"refreshed"`
		Names     []string `json:"names"`
		Skipped   []string `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Refreshed != 1 || len(payload.Names) != 1 || payload.Names[0] != "refreshable.json" {
		t.Fatalf("unexpected refreshed payload: %+v", payload)
	}
	if len(payload.Skipped) != 1 || payload.Skipped[0] != "no-rt.json" {
		t.Fatalf("unexpected skipped payload: %+v", payload)
	}

	updated, ok := manager.GetByID("refreshable")
	if !ok || updated == nil {
		t.Fatalf("expected refreshed auth to exist")
	}
	if updated.Disabled || updated.Status != coreauth.StatusActive || updated.Unavailable || updated.LastError != nil {
		t.Fatalf("refreshed auth state not cleared: %#v", updated)
	}
	if got := updated.Metadata["access_token"]; got != "new-access-token" {
		t.Fatalf("access token = %v, want new-access-token", got)
	}
	persisted := store.Last()
	if persisted == nil {
		t.Fatalf("expected persisted auth")
	}
	if persisted.ID != "refreshable" || persisted.Disabled || persisted.Status != coreauth.StatusActive {
		t.Fatalf("persisted auth = %#v, want active refreshed auth", persisted)
	}
	if got := persisted.Metadata["refresh_token"]; got != "new-refresh-token" {
		t.Fatalf("persisted refresh token = %v, want new-refresh-token", got)
	}
}
