package cliproxy

import (
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestRegisterModelsForAuth_CodexOAuthIgnoresPlanTypeTierRestrictions(t *testing.T) {
	service := &Service{}
	auth := &coreauth.Auth{
		ID:       "codex-oauth-free-auth",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"auth_kind": "oauth",
			"plan_type": "free",
		},
	}

	registry := GlobalModelRegistry()
	registry.UnregisterClient(auth.ID)
	t.Cleanup(func() {
		registry.UnregisterClient(auth.ID)
	})

	service.registerModelsForAuth(auth)

	if !registry.ClientSupportsModel(auth.ID, "gpt-5.3-codex") {
		t.Fatal("expected codex oauth auth to expose gpt-5.3-codex regardless of free plan_type")
	}
	if !registry.ClientSupportsModel(auth.ID, "gpt-5.4") {
		t.Fatal("expected codex oauth auth to expose gpt-5.4 regardless of free plan_type")
	}
}

func TestRegisterModelsForAuth_CodexAPIKeyStillHonorsPlanTypeTierRestrictions(t *testing.T) {
	service := &Service{}
	auth := &coreauth.Auth{
		ID:       "codex-apikey-free-auth",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"auth_kind": "apikey",
			"plan_type": "free",
		},
	}

	registry := GlobalModelRegistry()
	registry.UnregisterClient(auth.ID)
	t.Cleanup(func() {
		registry.UnregisterClient(auth.ID)
	})

	service.registerModelsForAuth(auth)

	if !registry.ClientSupportsModel(auth.ID, "gpt-5.2-codex") {
		t.Fatal("expected free codex api key auth to expose gpt-5.2-codex")
	}
	if registry.ClientSupportsModel(auth.ID, "gpt-5.3-codex") {
		t.Fatal("expected free codex api key auth to remain tier-limited for gpt-5.3-codex")
	}
	if registry.ClientSupportsModel(auth.ID, "gpt-5.4") {
		t.Fatal("expected free codex api key auth to remain tier-limited for gpt-5.4")
	}
}
