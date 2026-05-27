package codex

import (
	"fmt"
	"strings"
	"time"
)

// NormalizeAuthMetadata folds official Codex auth.json token data into the
// flattened metadata shape used by CLIProxyAPI auth stores.
func NormalizeAuthMetadata(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	changed := false

	tokenData, hasTokenData := objectMap(metadata["tokens"])
	if !hasTokenData {
		tokenData, hasTokenData = objectMap(metadata["token_data"])
	}

	if hasTokenData && strings.TrimSpace(stringValue(metadata["type"])) == "" && looksLikeCodexTokenData(tokenData) {
		metadata["type"] = "codex"
		changed = true
	}

	if hasTokenData {
		for _, key := range []string{"id_token", "access_token", "refresh_token"} {
			if setStringIfEmpty(metadata, key, stringValue(tokenData[key])) {
				changed = true
			}
		}
		for _, key := range []string{"account_id", "chatgpt_account_id", "email"} {
			if setStringIfEmpty(metadata, key, stringValue(tokenData[key])) {
				changed = true
			}
		}
	}

	if setStringIfEmpty(metadata, "last_refresh", firstString(metadata["last_refresh"], metadata["lastRefresh"])) {
		changed = true
	}

	accountSourceToken := strings.TrimSpace(firstString(metadata["id_token"], metadata["access_token"]))
	if accountSourceToken == "" {
		return changed
	}
	claims, err := ParseJWTToken(accountSourceToken)
	if err != nil || claims == nil {
		return changed
	}

	if setStringIfEmpty(metadata, "email", claims.GetUserEmail()) {
		changed = true
	}
	accountID := strings.TrimSpace(firstString(
		metadata["account_id"],
		metadata["chatgpt_account_id"],
		claims.GetAccountID(),
	))
	if accountID != "" {
		if setStringIfEmpty(metadata, "account_id", accountID) {
			changed = true
		}
		if setStringIfEmpty(metadata, "chatgpt_account_id", accountID) {
			changed = true
		}
	}
	if userID := strings.TrimSpace(firstString(claims.CodexAuthInfo.ChatgptUserID, claims.CodexAuthInfo.UserID)); userID != "" {
		if setStringIfEmpty(metadata, "chatgpt_user_id", userID) {
			changed = true
		}
	}
	if setStringIfEmpty(metadata, "plan_type", claims.CodexAuthInfo.ChatgptPlanType) {
		changed = true
	}
	if claims.Exp > 0 && strings.TrimSpace(stringValue(metadata["expired"])) == "" {
		metadata["expired"] = time.Unix(int64(claims.Exp), 0).UTC().Format(time.RFC3339)
		changed = true
	}
	return changed
}

func looksLikeCodexTokenData(tokenData map[string]any) bool {
	if tokenData == nil {
		return false
	}
	return strings.TrimSpace(stringValue(tokenData["id_token"])) != "" ||
		strings.TrimSpace(stringValue(tokenData["access_token"])) != "" ||
		strings.TrimSpace(stringValue(tokenData["refresh_token"])) != ""
}

func setStringIfEmpty(metadata map[string]any, key, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.TrimSpace(stringValue(metadata[key])) != "" {
		return false
	}
	metadata[key] = value
	return true
}

func firstString(values ...any) string {
	for _, value := range values {
		if s := strings.TrimSpace(stringValue(value)); s != "" {
			return s
		}
	}
	return ""
}

func objectMap(value any) (map[string]any, bool) {
	switch v := value.(type) {
	case map[string]any:
		return v, true
	case map[string]string:
		out := make(map[string]any, len(v))
		for key, val := range v {
			out[key] = val
		}
		return out, true
	default:
		return nil, false
	}
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}
