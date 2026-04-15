package cliproxy

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestServiceApplyCoreAuthAddOrUpdate_DeleteReAddDoesNotInheritStaleRuntimeState(t *testing.T) {
	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}

	authID := "service-stale-state-auth"
	modelID := "stale-model"
	lastRefreshedAt := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	nextRefreshAfter := lastRefreshedAt.Add(30 * time.Minute)

	t.Cleanup(func() {
		GlobalModelRegistry().UnregisterClient(authID)
	})

	service.applyCoreAuthAddOrUpdate(context.Background(), &coreauth.Auth{
		ID:               authID,
		Provider:         "claude",
		Status:           coreauth.StatusActive,
		LastRefreshedAt:  lastRefreshedAt,
		NextRefreshAfter: nextRefreshAfter,
		ModelStates: map[string]*coreauth.ModelState{
			modelID: {
				Quota: coreauth.QuotaState{BackoffLevel: 7},
			},
		},
	})

	service.applyCoreAuthRemoval(context.Background(), authID)

	disabled, ok := service.coreManager.GetByID(authID)
	if !ok || disabled == nil {
		t.Fatalf("expected disabled auth after removal")
	}
	if !disabled.Disabled || disabled.Status != coreauth.StatusDisabled {
		t.Fatalf("expected disabled auth after removal, got disabled=%v status=%v", disabled.Disabled, disabled.Status)
	}
	if disabled.LastRefreshedAt.IsZero() {
		t.Fatalf("expected disabled auth to still carry prior LastRefreshedAt for regression setup")
	}
	if disabled.NextRefreshAfter.IsZero() {
		t.Fatalf("expected disabled auth to still carry prior NextRefreshAfter for regression setup")
	}
	if len(disabled.ModelStates) == 0 {
		t.Fatalf("expected disabled auth to still carry prior ModelStates for regression setup")
	}

	service.applyCoreAuthAddOrUpdate(context.Background(), &coreauth.Auth{
		ID:       authID,
		Provider: "claude",
		Status:   coreauth.StatusActive,
	})

	updated, ok := service.coreManager.GetByID(authID)
	if !ok || updated == nil {
		t.Fatalf("expected re-added auth to be present")
	}
	if updated.Disabled {
		t.Fatalf("expected re-added auth to be active")
	}
	if !updated.LastRefreshedAt.IsZero() {
		t.Fatalf("expected LastRefreshedAt to reset on delete -> re-add, got %v", updated.LastRefreshedAt)
	}
	if !updated.NextRefreshAfter.IsZero() {
		t.Fatalf("expected NextRefreshAfter to reset on delete -> re-add, got %v", updated.NextRefreshAfter)
	}
	if len(updated.ModelStates) != 0 {
		t.Fatalf("expected ModelStates to reset on delete -> re-add, got %d entries", len(updated.ModelStates))
	}
	if models := registry.GetGlobalRegistry().GetModelsForClient(authID); len(models) == 0 {
		t.Fatalf("expected re-added auth to re-register models in global registry")
	}
}

func TestServiceApplyCoreAuthAddOrUpdate_PreservesExistingCooldownStateForFileRefresh(t *testing.T) {
	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}

	authID := "service-preserve-cooldown-auth"
	modelID := "gpt-5.4"
	now := time.Date(2026, time.April, 15, 12, 0, 0, 0, time.UTC)
	nextRetry := now.Add(45 * time.Minute)
	lastRefresh := now.Add(-30 * time.Minute)

	t.Cleanup(func() {
		GlobalModelRegistry().UnregisterClient(authID)
	})

	service.applyCoreAuthAddOrUpdate(context.Background(), &coreauth.Auth{
		ID:              authID,
		Provider:        "codex",
		Status:          coreauth.StatusError,
		StatusMessage:   "quota exhausted",
		Unavailable:     true,
		LastRefreshedAt: lastRefresh,
		NextRetryAfter:  nextRetry,
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "quota",
			NextRecoverAt: nextRetry,
			BackoffLevel:  4,
		},
		LastError: &coreauth.Error{
			Message:    "usage_limit_reached",
			HTTPStatus: 429,
		},
		ModelStates: map[string]*coreauth.ModelState{
			modelID: {
				Status:         coreauth.StatusError,
				StatusMessage:  "quota exhausted",
				Unavailable:    true,
				NextRetryAfter: nextRetry,
				LastError: &coreauth.Error{
					Message:    "usage_limit_reached",
					HTTPStatus: 429,
				},
				Quota: coreauth.QuotaState{
					Exceeded:      true,
					Reason:        "quota",
					NextRecoverAt: nextRetry,
					BackoffLevel:  2,
				},
			},
		},
	})

	service.applyCoreAuthAddOrUpdate(context.Background(), &coreauth.Auth{
		ID:       authID,
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"type": "codex", "email": "refresh@example.com"},
	})

	updated, ok := service.coreManager.GetByID(authID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if updated.Status != coreauth.StatusError {
		t.Fatalf("status = %s, want %s", updated.Status, coreauth.StatusError)
	}
	if updated.StatusMessage != "quota exhausted" {
		t.Fatalf("status message = %q, want %q", updated.StatusMessage, "quota exhausted")
	}
	if !updated.Unavailable {
		t.Fatalf("expected unavailable=true")
	}
	if !updated.NextRetryAfter.Equal(nextRetry) {
		t.Fatalf("next retry after = %v, want %v", updated.NextRetryAfter, nextRetry)
	}
	if !updated.LastRefreshedAt.Equal(lastRefresh) {
		t.Fatalf("last refreshed at = %v, want %v", updated.LastRefreshedAt, lastRefresh)
	}
	if !updated.Quota.Exceeded || updated.Quota.BackoffLevel != 4 || !updated.Quota.NextRecoverAt.Equal(nextRetry) {
		t.Fatalf("quota = %#v, want preserved cooldown state", updated.Quota)
	}
	if updated.LastError == nil || updated.LastError.HTTPStatus != 429 {
		t.Fatalf("last error = %#v, want preserved 429 state", updated.LastError)
	}
	state := updated.ModelStates[modelID]
	if state == nil {
		t.Fatalf("expected model state to be preserved")
	}
	if state.Status != coreauth.StatusError || !state.Unavailable || !state.NextRetryAfter.Equal(nextRetry) {
		t.Fatalf("model state = %#v, want preserved cooldown state", state)
	}
}
