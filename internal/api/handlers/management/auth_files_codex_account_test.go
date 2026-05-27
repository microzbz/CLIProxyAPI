package management

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestBuildAuthFileEntryForListCodexIDTokenFallsBackToOrganizationID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	path := filepath.Join(authDir, "codex-user.json")
	if err := os.WriteFile(path, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	idToken := managementTestJWT(t, map[string]any{
		"email": "user@example.com",
		"https://api.openai.com/auth": map[string]any{
			"user_id": "user-id",
			"organizations": []map[string]any{
				{"id": "org-secondary", "is_default": false},
				{"id": "org-default", "is_default": true},
			},
		},
	})
	auth := &coreauth.Auth{
		ID:       "codex-user",
		FileName: "codex-user.json",
		Provider: "codex",
		Attributes: map[string]string{
			"path": path,
		},
		Metadata: map[string]any{
			"type":     "codex",
			"id_token": idToken,
		},
	}

	entry := (&Handler{}).buildAuthFileEntryForList(auth, false)
	if entry == nil {
		t.Fatal("buildAuthFileEntryForList() = nil")
	}
	if got := valueAsString(entry["account_id"]); got != "org-default" {
		t.Fatalf("account_id = %q, want org-default", got)
	}
	if got := valueAsString(entry["chatgpt_account_id"]); got != "org-default" {
		t.Fatalf("chatgpt_account_id = %q, want org-default", got)
	}
	claims, ok := entry["id_token"].(gin.H)
	if !ok {
		t.Fatalf("id_token = %T, want gin.H", entry["id_token"])
	}
	if got := valueAsString(claims["chatgpt_account_id"]); got != "org-default" {
		t.Fatalf("id_token.chatgpt_account_id = %q, want org-default", got)
	}
	if got := valueAsString(claims["chatgpt_user_id"]); got != "user-id" {
		t.Fatalf("id_token.chatgpt_user_id = %q, want user-id", got)
	}
}

func managementTestJWT(t *testing.T, claims map[string]any) string {
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
