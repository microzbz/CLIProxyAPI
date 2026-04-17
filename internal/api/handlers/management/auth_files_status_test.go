package management

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestPatchAuthFileStatus_EnableClearsCooldownStateAndRegistry(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(nil, nil, nil)
	now := time.Now()
	next := now.Add(30 * time.Minute)

	auth := &coreauth.Auth{
		ID:             "codex-auth",
		FileName:       "codex-auth.json",
		Provider:       "codex",
		Status:         coreauth.StatusDisabled,
		StatusMessage:  "disabled via management API",
		Disabled:       true,
		Unavailable:    true,
		NextRetryAfter: next,
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "codex_usage_limit_reached",
			NextRecoverAt: next,
		},
		LocalRateLimit: coreauth.LocalRateLimitState{
			RequestTimestamps: []time.Time{now.Add(-10 * time.Second)},
			CooldownUntil:     next,
		},
		LastError: &coreauth.Error{
			HTTPStatus: http.StatusTooManyRequests,
			Message:    "usage_limit_reached",
		},
		ModelStates: map[string]*coreauth.ModelState{
			"gpt-5.4": {
				Status:         coreauth.StatusError,
				StatusMessage:  "quota exhausted",
				Unavailable:    true,
				NextRetryAfter: next,
				LastError: &coreauth.Error{
					HTTPStatus: http.StatusTooManyRequests,
					Message:    "usage_limit_reached",
				},
				Quota: coreauth.QuotaState{
					Exceeded:      true,
					Reason:        "quota",
					NextRecoverAt: next,
				},
			},
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "gpt-5.4"}})
	t.Cleanup(func() {
		reg.UnregisterClient(auth.ID)
	})
	reg.SetModelQuotaExceeded(auth.ID, "gpt-5.4")
	reg.SuspendClientModel(auth.ID, "gpt-5.4", "quota")

	if got := reg.GetModelCount("gpt-5.4"); got != 0 {
		t.Fatalf("model count before enable = %d, want 0", got)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(
		http.MethodPatch,
		"/v0/management/auth-files/status",
		bytes.NewBufferString(`{"name":"codex-auth.json","disabled":false}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PatchAuthFileStatus(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected updated auth to exist")
	}
	if updated.Disabled {
		t.Fatalf("expected auth to be enabled")
	}
	if updated.Status != coreauth.StatusActive {
		t.Fatalf("status = %s, want %s", updated.Status, coreauth.StatusActive)
	}
	if updated.StatusMessage != "" {
		t.Fatalf("status message = %q, want empty", updated.StatusMessage)
	}
	if updated.Unavailable {
		t.Fatalf("expected auth unavailable=false")
	}
	if !updated.NextRetryAfter.IsZero() {
		t.Fatalf("next retry after = %v, want zero", updated.NextRetryAfter)
	}
	if updated.LastError != nil {
		t.Fatalf("last error = %#v, want nil", updated.LastError)
	}
	if updated.Quota.Exceeded || updated.Quota.Reason != "" || !updated.Quota.NextRecoverAt.IsZero() {
		t.Fatalf("quota = %#v, want cleared", updated.Quota)
	}
	if len(updated.LocalRateLimit.RequestTimestamps) != 0 || !updated.LocalRateLimit.CooldownUntil.IsZero() {
		t.Fatalf("local rate limit = %#v, want cleared", updated.LocalRateLimit)
	}

	state := updated.ModelStates["gpt-5.4"]
	if state == nil {
		t.Fatalf("expected model state to remain present")
	}
	if state.Status != coreauth.StatusActive {
		t.Fatalf("model status = %s, want %s", state.Status, coreauth.StatusActive)
	}
	if state.StatusMessage != "" {
		t.Fatalf("model status message = %q, want empty", state.StatusMessage)
	}
	if state.Unavailable {
		t.Fatalf("expected model unavailable=false")
	}
	if !state.NextRetryAfter.IsZero() {
		t.Fatalf("model next retry after = %v, want zero", state.NextRetryAfter)
	}
	if state.LastError != nil {
		t.Fatalf("model last error = %#v, want nil", state.LastError)
	}
	if state.Quota.Exceeded || state.Quota.Reason != "" || !state.Quota.NextRecoverAt.IsZero() {
		t.Fatalf("model quota = %#v, want cleared", state.Quota)
	}

	if got := reg.GetModelCount("gpt-5.4"); got != 1 {
		t.Fatalf("model count after enable = %d, want 1", got)
	}
}

func TestPatchAuthFileStatus_BatchEnableClearsCooldownState(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(nil, nil, nil)
	now := time.Now()
	next := now.Add(15 * time.Minute)

	auths := []*coreauth.Auth{
		{
			ID:             "auth-a",
			FileName:       "auth-a.json",
			Provider:       "codex",
			Status:         coreauth.StatusError,
			StatusMessage:  "cooldown",
			Unavailable:    true,
			NextRetryAfter: next,
			Quota: coreauth.QuotaState{
				Exceeded:      true,
				Reason:        "quota",
				NextRecoverAt: next,
			},
			LocalRateLimit: coreauth.LocalRateLimitState{
				RequestTimestamps: []time.Time{now.Add(-5 * time.Second)},
				CooldownUntil:     next,
			},
		},
		{
			ID:             "auth-b",
			FileName:       "auth-b.json",
			Provider:       "codex",
			Status:         coreauth.StatusError,
			StatusMessage:  "cooldown",
			Unavailable:    true,
			NextRetryAfter: next,
			LocalRateLimit: coreauth.LocalRateLimitState{
				RequestTimestamps: []time.Time{now.Add(-8 * time.Second)},
				CooldownUntil:     next,
			},
		},
	}

	reg := registry.GetGlobalRegistry()
	for _, auth := range auths {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register %s: %v", auth.ID, err)
		}
		reg.RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "gpt-5.4"}})
		reg.SetModelQuotaExceeded(auth.ID, "gpt-5.4")
		reg.SuspendClientModel(auth.ID, "gpt-5.4", "quota")
	}
	t.Cleanup(func() {
		for _, auth := range auths {
			reg.UnregisterClient(auth.ID)
		}
	})

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(
		http.MethodPatch,
		"/v0/management/auth-files/status",
		bytes.NewBufferString(`{"names":["auth-a.json","auth-b.json"],"disabled":false}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PatchAuthFileStatus(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	for _, authID := range []string{"auth-a", "auth-b"} {
		updated, ok := manager.GetByID(authID)
		if !ok || updated == nil {
			t.Fatalf("expected auth %s to exist", authID)
		}
		if updated.Unavailable {
			t.Fatalf("expected auth %s unavailable=false", authID)
		}
		if !updated.NextRetryAfter.IsZero() {
			t.Fatalf("auth %s next retry after = %v, want zero", authID, updated.NextRetryAfter)
		}
		if updated.Quota.Exceeded || !updated.Quota.NextRecoverAt.IsZero() {
			t.Fatalf("auth %s quota = %#v, want cleared", authID, updated.Quota)
		}
		if len(updated.LocalRateLimit.RequestTimestamps) != 0 || !updated.LocalRateLimit.CooldownUntil.IsZero() {
			t.Fatalf("auth %s local rate limit = %#v, want cleared", authID, updated.LocalRateLimit)
		}
	}
}

func TestPatchAuthFileStatus_FilteredEnableByProviderAndState(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(nil, nil, nil)
	now := time.Now()
	next := now.Add(20 * time.Minute)

	register := func(auth *coreauth.Auth) {
		t.Helper()
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register %s: %v", auth.ID, err)
		}
	}

	register(&coreauth.Auth{
		ID:             "codex-cooldown",
		FileName:       "codex-cooldown.json",
		Provider:       "codex",
		Status:         coreauth.StatusError,
		StatusMessage:  "cooldown",
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
		StatusMessage:  "cooldown",
		Unavailable:    true,
		NextRetryAfter: next,
	})

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(
		http.MethodPatch,
		"/v0/management/auth-files/status",
		bytes.NewBufferString(`{"provider":"codex","state":"cooldown","disabled":false}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PatchAuthFileStatus(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	codexCooldown, _ := manager.GetByID("codex-cooldown")
	if codexCooldown == nil || codexCooldown.Unavailable || !codexCooldown.NextRetryAfter.IsZero() {
		t.Fatalf("expected codex cooldown auth to be cleared, got %#v", codexCooldown)
	}
	codexActive, _ := manager.GetByID("codex-active")
	if codexActive == nil || codexActive.Status != coreauth.StatusActive {
		t.Fatalf("expected codex active auth to remain active, got %#v", codexActive)
	}
	claudeCooldown, _ := manager.GetByID("claude-cooldown")
	if claudeCooldown == nil || !claudeCooldown.Unavailable || claudeCooldown.NextRetryAfter.IsZero() {
		t.Fatalf("expected claude cooldown auth to remain blocked, got %#v", claudeCooldown)
	}
}
