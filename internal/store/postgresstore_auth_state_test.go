package store

import (
	"encoding/json"
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestDecodePersistedAuthPayloadRoundTripPreservesRuntimeState(t *testing.T) {
	metadataPayload := []byte(`{"type":"codex","email":"roundtrip@example.com","note":"keep"}`)
	createdAt := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(5 * time.Minute)
	nextRetry := createdAt.Add(30 * time.Minute)
	lastRefresh := createdAt.Add(-2 * time.Hour)

	auth := &cliproxyauth.Auth{
		ID:              "auths/roundtrip.json",
		Provider:        "codex",
		Prefix:          "team-a",
		FileName:        "auths/roundtrip.json",
		Label:           "legacy-label",
		Status:          cliproxyauth.StatusError,
		StatusMessage:   "quota exhausted",
		Disabled:        true,
		Unavailable:     true,
		ProxyURL:        "http://127.0.0.1:8888",
		Attributes:      map[string]string{"path": "/old/auths/roundtrip.json", "source": "/old/auths/roundtrip.json", "priority": "9"},
		Metadata:        map[string]any{"type": "codex", "email": "roundtrip@example.com"},
		Quota:           cliproxyauth.QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: nextRetry, BackoffLevel: 3},
		LastError:       &cliproxyauth.Error{Code: "rate_limit", Message: "too many requests", Retryable: true, HTTPStatus: 429},
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		LastRefreshedAt: lastRefresh,
		NextRetryAfter:  nextRetry,
		ModelStates: map[string]*cliproxyauth.ModelState{
			"gpt-5.4": {
				Status:         cliproxyauth.StatusError,
				StatusMessage:  "quota exhausted",
				Unavailable:    true,
				NextRetryAfter: nextRetry,
				LastError:      &cliproxyauth.Error{Message: "too many requests", HTTPStatus: 429},
				Quota:          cliproxyauth.QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: nextRetry, BackoffLevel: 2},
				UpdatedAt:      updatedAt,
			},
		},
	}

	payload, err := encodePersistedAuthRecord(auth, metadataPayload)
	if err != nil {
		t.Fatalf("encodePersistedAuthRecord() error = %v", err)
	}

	decoded, rawMetadata, err := decodePersistedAuthPayload(
		"auths/roundtrip.json",
		payload,
		createdAt.Add(-24*time.Hour),
		updatedAt.Add(-24*time.Hour),
		"/tmp/pgstore/auths/auths/roundtrip.json",
	)
	if err != nil {
		t.Fatalf("decodePersistedAuthPayload() error = %v", err)
	}

	if !jsonEqual(rawMetadata, metadataPayload) {
		t.Fatalf("raw metadata changed: got=%s want=%s", string(rawMetadata), string(metadataPayload))
	}
	if decoded.ID != "auths/roundtrip.json" {
		t.Fatalf("decoded.ID = %q, want %q", decoded.ID, "auths/roundtrip.json")
	}
	if decoded.Provider != "codex" {
		t.Fatalf("decoded.Provider = %q, want %q", decoded.Provider, "codex")
	}
	if decoded.Label != "roundtrip@example.com" {
		t.Fatalf("decoded.Label = %q, want %q", decoded.Label, "roundtrip@example.com")
	}
	if decoded.Prefix != "team-a" {
		t.Fatalf("decoded.Prefix = %q, want %q", decoded.Prefix, "team-a")
	}
	if decoded.Status != cliproxyauth.StatusError {
		t.Fatalf("decoded.Status = %q, want %q", decoded.Status, cliproxyauth.StatusError)
	}
	if !decoded.Disabled || !decoded.Unavailable {
		t.Fatalf("decoded availability flags = disabled:%v unavailable:%v, want both true", decoded.Disabled, decoded.Unavailable)
	}
	if decoded.ProxyURL != "http://127.0.0.1:8888" {
		t.Fatalf("decoded.ProxyURL = %q, want proxy url", decoded.ProxyURL)
	}
	if decoded.Attributes["path"] != "/tmp/pgstore/auths/auths/roundtrip.json" {
		t.Fatalf("decoded.Attributes[path] = %q, want updated spool path", decoded.Attributes["path"])
	}
	if decoded.Attributes["priority"] != "9" {
		t.Fatalf("decoded.Attributes[priority] = %q, want %q", decoded.Attributes["priority"], "9")
	}
	if decoded.Attributes["source"] != "" {
		t.Fatalf("decoded.Attributes[source] = %q, want empty after path stripping", decoded.Attributes["source"])
	}
	if decoded.Quota.BackoffLevel != 3 || !decoded.Quota.Exceeded {
		t.Fatalf("decoded.Quota = %#v, want preserved quota state", decoded.Quota)
	}
	if decoded.LastError == nil || decoded.LastError.HTTPStatus != 429 {
		t.Fatalf("decoded.LastError = %#v, want preserved error", decoded.LastError)
	}
	if !decoded.CreatedAt.Equal(createdAt) || !decoded.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("decoded timestamps = created:%v updated:%v, want %v %v", decoded.CreatedAt, decoded.UpdatedAt, createdAt, updatedAt)
	}
	if !decoded.LastRefreshedAt.Equal(lastRefresh) || !decoded.NextRetryAfter.Equal(nextRetry) {
		t.Fatalf("decoded refresh timestamps = last:%v retry:%v, want %v %v", decoded.LastRefreshedAt, decoded.NextRetryAfter, lastRefresh, nextRetry)
	}
	state := decoded.ModelStates["gpt-5.4"]
	if state == nil {
		t.Fatalf("decoded.ModelStates[gpt-5.4] missing")
	}
	if state.Status != cliproxyauth.StatusError || !state.Unavailable || state.Quota.BackoffLevel != 2 {
		t.Fatalf("decoded model state = %#v, want preserved runtime state", state)
	}
}

func TestDecodePersistedAuthPayloadLegacyMetadataUsesDefaults(t *testing.T) {
	createdAt := time.Date(2026, 4, 15, 11, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(10 * time.Minute)
	legacyPayload := []byte(`{"type":"codex","email":"legacy@example.com","disabled":true}`)

	auth, rawMetadata, err := decodePersistedAuthPayload(
		"legacy.json",
		legacyPayload,
		createdAt,
		updatedAt,
		"/tmp/pgstore/auths/legacy.json",
	)
	if err != nil {
		t.Fatalf("decodePersistedAuthPayload() error = %v", err)
	}
	if !jsonEqual(rawMetadata, legacyPayload) {
		t.Fatalf("raw metadata changed: got=%s want=%s", string(rawMetadata), string(legacyPayload))
	}
	if auth.Provider != "codex" {
		t.Fatalf("auth.Provider = %q, want %q", auth.Provider, "codex")
	}
	if auth.Label != "legacy@example.com" {
		t.Fatalf("auth.Label = %q, want %q", auth.Label, "legacy@example.com")
	}
	if !auth.Disabled {
		t.Fatalf("auth.Disabled = false, want true")
	}
	if auth.Status != cliproxyauth.StatusDisabled {
		t.Fatalf("auth.Status = %q, want %q", auth.Status, cliproxyauth.StatusDisabled)
	}
	if auth.Attributes["path"] != "/tmp/pgstore/auths/legacy.json" {
		t.Fatalf("auth.Attributes[path] = %q, want spool path", auth.Attributes["path"])
	}
	if auth.Attributes["email"] != "legacy@example.com" {
		t.Fatalf("auth.Attributes[email] = %q, want %q", auth.Attributes["email"], "legacy@example.com")
	}
	if !auth.CreatedAt.Equal(createdAt) || !auth.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("auth timestamps = created:%v updated:%v, want %v %v", auth.CreatedAt, auth.UpdatedAt, createdAt, updatedAt)
	}
}

func TestParseAuthMetadataPayloadRejectsInvalidJSON(t *testing.T) {
	if _, _, err := parseAuthMetadataPayload([]byte(`{"type"`)); err == nil {
		t.Fatalf("parseAuthMetadataPayload() error = nil, want non-nil")
	}
}

func TestMetadataBoolSupportsMultipleTypes(t *testing.T) {
	payload := map[string]any{
		"bool_true":   true,
		"string_true": "true",
		"number_true": json.Number("1"),
	}
	if value, ok := metadataBool(payload, "bool_true"); !ok || !value {
		t.Fatalf("metadataBool(bool_true) = %v, %v, want true, true", value, ok)
	}
	if value, ok := metadataBool(payload, "string_true"); !ok || !value {
		t.Fatalf("metadataBool(string_true) = %v, %v, want true, true", value, ok)
	}
	if value, ok := metadataBool(payload, "number_true"); !ok || !value {
		t.Fatalf("metadataBool(number_true) = %v, %v, want true, true", value, ok)
	}
}
