package codex

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeAuthMetadataOfficialCodexAuthJSON(t *testing.T) {
	idToken := testJWT(t, map[string]any{
		"email": "user@example.com",
		"exp":   int(time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC).Unix()),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-from-jwt",
			"chatgpt_plan_type":  "plus",
		},
	})
	metadata := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"id_token":      idToken,
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"account_id":    "acct-from-token",
		},
		"last_refresh": "2026-05-27T00:00:00Z",
	}

	if !NormalizeAuthMetadata(metadata) {
		t.Fatal("NormalizeAuthMetadata() = false, want true")
	}
	if got := metadata["type"]; got != "codex" {
		t.Fatalf("type = %v, want codex", got)
	}
	if got := metadata["account_id"]; got != "acct-from-token" {
		t.Fatalf("account_id = %v, want acct-from-token", got)
	}
	if got := metadata["chatgpt_account_id"]; got != "acct-from-token" {
		t.Fatalf("chatgpt_account_id = %v, want acct-from-token", got)
	}
	if got := metadata["email"]; got != "user@example.com" {
		t.Fatalf("email = %v, want user@example.com", got)
	}
	if got := metadata["access_token"]; got != "access-token" {
		t.Fatalf("access_token = %v, want access-token", got)
	}
}

func TestNormalizeAuthMetadataDerivesAccountIDFromIDToken(t *testing.T) {
	idToken := testJWT(t, map[string]any{
		"email": "user@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-from-jwt",
			"chatgpt_plan_type":  "pro",
		},
	})
	metadata := map[string]any{
		"type":         "codex",
		"id_token":     idToken,
		"access_token": "access-token",
	}

	if !NormalizeAuthMetadata(metadata) {
		t.Fatal("NormalizeAuthMetadata() = false, want true")
	}
	if got := metadata["account_id"]; got != "acct-from-jwt" {
		t.Fatalf("account_id = %v, want acct-from-jwt", got)
	}
	if got := metadata["chatgpt_account_id"]; got != "acct-from-jwt" {
		t.Fatalf("chatgpt_account_id = %v, want acct-from-jwt", got)
	}
	if got := metadata["plan_type"]; got != "pro" {
		t.Fatalf("plan_type = %v, want pro", got)
	}
}

func TestNormalizeAuthMetadataFallsBackToOrganizationID(t *testing.T) {
	idToken := testJWT(t, map[string]any{
		"email": "user@example.com",
		"https://api.openai.com/auth": map[string]any{
			"user_id": "user-id",
			"organizations": []map[string]any{
				{"id": "org-secondary", "is_default": false},
				{"id": "org-default", "is_default": true},
			},
		},
	})
	metadata := map[string]any{
		"type":         "codex",
		"id_token":     idToken,
		"access_token": "access-token",
	}

	if !NormalizeAuthMetadata(metadata) {
		t.Fatal("NormalizeAuthMetadata() = false, want true")
	}
	if got := metadata["account_id"]; got != "org-default" {
		t.Fatalf("account_id = %v, want org-default", got)
	}
	if got := metadata["chatgpt_user_id"]; got != "user-id" {
		t.Fatalf("chatgpt_user_id = %v, want user-id", got)
	}
}

func TestNormalizeAuthMetadataFallsBackToUserID(t *testing.T) {
	idToken := testJWT(t, map[string]any{
		"email": "user@example.com",
		"https://api.openai.com/auth": map[string]any{
			"user_id": "user-id",
		},
	})
	metadata := map[string]any{
		"type":         "codex",
		"id_token":     idToken,
		"access_token": "access-token",
	}

	if !NormalizeAuthMetadata(metadata) {
		t.Fatal("NormalizeAuthMetadata() = false, want true")
	}
	if got := metadata["account_id"]; got != "user-id" {
		t.Fatalf("account_id = %v, want user-id", got)
	}
	if got := metadata["chatgpt_account_id"]; got != "user-id" {
		t.Fatalf("chatgpt_account_id = %v, want user-id", got)
	}
}

func testJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
