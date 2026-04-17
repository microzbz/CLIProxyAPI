package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	codexauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

const persistedAuthRecordVersion = 2

type persistedAuthRecord struct {
	RecordVersion int                 `json:"_pgstore_auth_record_version"`
	Metadata      json.RawMessage     `json:"metadata"`
	State         *persistedAuthState `json:"state,omitempty"`
}

type persistedAuthState struct {
	ID               string                              `json:"id,omitempty"`
	Provider         string                              `json:"provider,omitempty"`
	Prefix           string                              `json:"prefix,omitempty"`
	FileName         string                              `json:"file_name,omitempty"`
	Label            string                              `json:"label,omitempty"`
	Status           cliproxyauth.Status                 `json:"status,omitempty"`
	StatusMessage    string                              `json:"status_message,omitempty"`
	Disabled         bool                                `json:"disabled,omitempty"`
	Unavailable      bool                                `json:"unavailable,omitempty"`
	ProxyURL         string                              `json:"proxy_url,omitempty"`
	Attributes       map[string]string                   `json:"attributes,omitempty"`
	Quota            cliproxyauth.QuotaState             `json:"quota,omitempty"`
	LastError        *cliproxyauth.Error                 `json:"last_error,omitempty"`
	CreatedAt        time.Time                           `json:"created_at,omitempty"`
	UpdatedAt        time.Time                           `json:"updated_at,omitempty"`
	LastRefreshedAt  time.Time                           `json:"last_refreshed_at,omitempty"`
	NextRefreshAfter time.Time                           `json:"next_refresh_after,omitempty"`
	NextRetryAfter   time.Time                           `json:"next_retry_after,omitempty"`
	LocalRateLimit   cliproxyauth.LocalRateLimitState    `json:"local_rate_limit,omitempty"`
	ModelStates      map[string]*cliproxyauth.ModelState `json:"model_states,omitempty"`
}

func encodePersistedAuthRecord(auth *cliproxyauth.Auth, metadataPayload []byte) ([]byte, error) {
	rawMetadata, metadata, err := parseAuthMetadataPayload(metadataPayload)
	if err != nil {
		return nil, err
	}
	snapshot := auth
	if snapshot == nil {
		snapshot = defaultAuthFromMetadata("", "", metadata, time.Time{}, time.Time{})
	} else {
		snapshot = snapshot.Clone()
		snapshot.Metadata = metadata
	}
	record := persistedAuthRecord{
		RecordVersion: persistedAuthRecordVersion,
		Metadata:      rawMetadata,
		State:         newPersistedAuthState(snapshot),
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("postgres store: marshal persisted auth record: %w", err)
	}
	return payload, nil
}

func decodePersistedAuthPayload(id string, payload []byte, createdAt, updatedAt time.Time, path string) (*cliproxyauth.Auth, json.RawMessage, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil, nil, fmt.Errorf("postgres store: empty auth payload for %s", id)
	}

	var record persistedAuthRecord
	if err := json.Unmarshal(trimmed, &record); err == nil && record.RecordVersion > 0 && len(bytes.TrimSpace(record.Metadata)) > 0 {
		rawMetadata, metadata, errMeta := parseAuthMetadataPayload(record.Metadata)
		if errMeta != nil {
			return nil, nil, errMeta
		}
		auth := buildAuthFromMetadataAndState(id, path, metadata, record.State, createdAt, updatedAt)
		return auth, rawMetadata, nil
	}

	rawMetadata, metadata, err := parseAuthMetadataPayload(trimmed)
	if err != nil {
		return nil, nil, err
	}
	return defaultAuthFromMetadata(id, path, metadata, createdAt, updatedAt), rawMetadata, nil
}

func (s *PostgresStore) buildPersistedAuthPayload(ctx context.Context, relID string, metadataPayload []byte, snapshot *cliproxyauth.Auth) ([]byte, error) {
	rawMetadata, metadata, err := parseAuthMetadataPayload(metadataPayload)
	if err != nil {
		return nil, err
	}

	var auth *cliproxyauth.Auth
	if snapshot != nil {
		auth = snapshot.Clone()
		auth.Metadata = metadata
	} else {
		auth, err = s.loadPersistedAuthForMetadata(ctx, relID, metadata)
		if err != nil {
			return nil, err
		}
	}
	normalizePersistedAuth(auth, relID, metadata)
	return encodePersistedAuthRecord(auth, rawMetadata)
}

func (s *PostgresStore) loadPersistedAuthForMetadata(ctx context.Context, relID string, metadata map[string]any) (*cliproxyauth.Auth, error) {
	query := fmt.Sprintf("SELECT content, created_at, updated_at FROM %s WHERE id = $1", s.fullTableName(s.cfg.AuthTable))
	var (
		payload   string
		createdAt time.Time
		updatedAt time.Time
	)
	err := s.db.QueryRowContext(ctx, query, relID).Scan(&payload, &createdAt, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return defaultAuthFromMetadata(relID, "", metadata, time.Time{}, time.Time{}), nil
	case err != nil:
		return nil, fmt.Errorf("postgres store: load existing auth state: %w", err)
	}

	auth, _, err := decodePersistedAuthPayload(relID, []byte(payload), createdAt, updatedAt, "")
	if err != nil {
		return nil, err
	}
	newProvider := strings.TrimSpace(valueAsString(metadata["type"]))
	if newProvider != "" && auth != nil && strings.TrimSpace(auth.Provider) != "" && !strings.EqualFold(strings.TrimSpace(auth.Provider), newProvider) {
		return defaultAuthFromMetadata(relID, "", metadata, time.Time{}, time.Time{}), nil
	}
	auth.Metadata = metadata
	normalizePersistedAuth(auth, relID, metadata)
	return auth, nil
}

func newPersistedAuthState(auth *cliproxyauth.Auth) *persistedAuthState {
	if auth == nil {
		return nil
	}
	attrs := cloneStringMap(auth.Attributes)
	if len(attrs) > 0 {
		delete(attrs, "path")
		if source := strings.TrimSpace(attrs["source"]); source != "" && strings.TrimSpace(authAttributePath(auth)) != "" && source == strings.TrimSpace(authAttributePath(auth)) {
			delete(attrs, "source")
		}
		if len(attrs) == 0 {
			attrs = nil
		}
	}
	return &persistedAuthState{
		ID:               strings.TrimSpace(auth.ID),
		Provider:         strings.TrimSpace(auth.Provider),
		Prefix:           strings.TrimSpace(auth.Prefix),
		FileName:         strings.TrimSpace(auth.FileName),
		Label:            strings.TrimSpace(auth.Label),
		Status:           auth.Status,
		StatusMessage:    auth.StatusMessage,
		Disabled:         auth.Disabled,
		Unavailable:      auth.Unavailable,
		ProxyURL:         strings.TrimSpace(auth.ProxyURL),
		Attributes:       attrs,
		Quota:            auth.Quota,
		LastError:        cloneAuthError(auth.LastError),
		CreatedAt:        auth.CreatedAt,
		UpdatedAt:        auth.UpdatedAt,
		LastRefreshedAt:  auth.LastRefreshedAt,
		NextRefreshAfter: auth.NextRefreshAfter,
		NextRetryAfter:   auth.NextRetryAfter,
		LocalRateLimit:   cloneLocalRateLimitState(auth.LocalRateLimit),
		ModelStates:      cloneModelStates(auth.ModelStates),
	}
}

func buildAuthFromMetadataAndState(relID, path string, metadata map[string]any, state *persistedAuthState, createdAt, updatedAt time.Time) *cliproxyauth.Auth {
	auth := defaultAuthFromMetadata(relID, path, metadata, createdAt, updatedAt)
	if state == nil {
		return auth
	}

	if strings.TrimSpace(auth.Provider) == "" || strings.EqualFold(auth.Provider, "unknown") {
		if provider := strings.TrimSpace(state.Provider); provider != "" {
			auth.Provider = provider
		}
	}
	if auth.Label == "" {
		auth.Label = strings.TrimSpace(state.Label)
	}
	if fileName := strings.TrimSpace(state.FileName); fileName != "" {
		auth.FileName = fileName
	}
	auth.Prefix = strings.TrimSpace(state.Prefix)
	auth.Status = state.Status
	auth.StatusMessage = state.StatusMessage
	auth.Disabled = state.Disabled
	auth.Unavailable = state.Unavailable
	auth.ProxyURL = strings.TrimSpace(state.ProxyURL)
	auth.Quota = state.Quota
	auth.LastError = cloneAuthError(state.LastError)
	auth.LastRefreshedAt = state.LastRefreshedAt
	auth.NextRefreshAfter = state.NextRefreshAfter
	auth.NextRetryAfter = state.NextRetryAfter
	auth.LocalRateLimit = cloneLocalRateLimitState(state.LocalRateLimit)
	auth.ModelStates = cloneModelStates(state.ModelStates)
	if !state.CreatedAt.IsZero() {
		auth.CreatedAt = state.CreatedAt
	}
	if !state.UpdatedAt.IsZero() {
		auth.UpdatedAt = state.UpdatedAt
	}

	attrs := cloneStringMap(state.Attributes)
	if len(attrs) == 0 {
		attrs = make(map[string]string, len(auth.Attributes))
	}
	for key, value := range auth.Attributes {
		attrs[key] = value
	}
	auth.Attributes = attrs
	normalizePersistedAuth(auth, relID, metadata)
	return auth
}

func defaultAuthFromMetadata(relID, path string, metadata map[string]any, createdAt, updatedAt time.Time) *cliproxyauth.Auth {
	provider := strings.TrimSpace(valueAsString(metadata["type"]))
	if provider == "" {
		provider = "unknown"
	}
	normalizedID := strings.TrimSpace(relID)
	normalizedFileName := strings.TrimSpace(relID)
	if normalizedID != "" {
		normalizedID = normalizeAuthID(relID)
		normalizedFileName = normalizeAuthID(relID)
	}
	attr := map[string]string{}
	if path != "" {
		attr["path"] = path
	}
	if email := strings.TrimSpace(valueAsString(metadata["email"])); email != "" {
		attr["email"] = email
	}
	auth := &cliproxyauth.Auth{
		ID:         normalizedID,
		Provider:   provider,
		FileName:   normalizedFileName,
		Label:      labelFor(metadata),
		Status:     cliproxyauth.StatusActive,
		Attributes: attr,
		Metadata:   metadata,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}
	if disabled, ok := metadataBool(metadata, "disabled"); ok {
		auth.Disabled = disabled
		if disabled {
			auth.Status = cliproxyauth.StatusDisabled
		}
	}
	return auth
}

func normalizePersistedAuth(auth *cliproxyauth.Auth, relID string, metadata map[string]any) {
	if auth == nil {
		return
	}
	auth.ID = normalizeAuthID(relID)
	if strings.TrimSpace(auth.FileName) == "" {
		auth.FileName = normalizeAuthID(relID)
	}
	if provider := strings.TrimSpace(valueAsString(metadata["type"])); provider != "" {
		auth.Provider = provider
	} else if strings.TrimSpace(auth.Provider) == "" {
		auth.Provider = "unknown"
	}
	if label := labelFor(metadata); label != "" {
		auth.Label = label
	}
	auth.Metadata = metadata
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	if email := strings.TrimSpace(valueAsString(metadata["email"])); email != "" {
		auth.Attributes["email"] = email
	} else {
		delete(auth.Attributes, "email")
	}
	applyMetadataDerivedAttributes(auth, metadata)
	if auth.Status == "" {
		if auth.Disabled {
			auth.Status = cliproxyauth.StatusDisabled
		} else {
			auth.Status = cliproxyauth.StatusActive
		}
	}
}

func applyMetadataDerivedAttributes(auth *cliproxyauth.Auth, metadata map[string]any) {
	if auth == nil || auth.Attributes == nil {
		return
	}

	if priority, ok := metadataIntString(metadata, "priority"); ok {
		auth.Attributes["priority"] = priority
	}
	if note := strings.TrimSpace(valueAsString(metadata["note"])); note != "" {
		auth.Attributes["note"] = note
	}
	if limit, ok := metadataIntString(metadata, "auth_rate_limit_limit"); ok {
		auth.Attributes["auth_rate_limit_limit"] = limit
	} else if limit, ok := metadataIntString(metadata, "auth-rate-limit-limit"); ok {
		auth.Attributes["auth_rate_limit_limit"] = limit
	}

	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	if provider == "codex" && strings.TrimSpace(auth.Attributes["auth_kind"]) == "" {
		auth.Attributes["auth_kind"] = "oauth"
	}
	if provider == "codex" && strings.TrimSpace(auth.Attributes["plan_type"]) == "" {
		if idTokenRaw := strings.TrimSpace(valueAsString(metadata["id_token"])); idTokenRaw != "" {
			if claims, err := codexauth.ParseJWTToken(idTokenRaw); err == nil && claims != nil {
				if planType := strings.TrimSpace(claims.CodexAuthInfo.ChatgptPlanType); planType != "" {
					auth.Attributes["plan_type"] = planType
				}
			}
		}
	}
}

func metadataIntString(metadata map[string]any, key string) (string, bool) {
	if len(metadata) == 0 {
		return "", false
	}
	raw, ok := metadata[key]
	if !ok {
		return "", false
	}
	switch value := raw.(type) {
	case float64:
		return strconv.Itoa(int(value)), true
	case float32:
		return strconv.Itoa(int(value)), true
	case int:
		return strconv.Itoa(value), true
	case int64:
		return strconv.FormatInt(value, 10), true
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return strconv.FormatInt(parsed, 10), true
		}
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "", false
		}
		if _, err := strconv.Atoi(trimmed); err == nil {
			return trimmed, true
		}
	}
	return "", false
}

func parseAuthMetadataPayload(payload []byte) (json.RawMessage, map[string]any, error) {
	raw := bytes.TrimSpace(payload)
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf("postgres store: auth metadata is empty")
	}
	metadata := make(map[string]any)
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, nil, fmt.Errorf("postgres store: invalid auth metadata json: %w", err)
	}
	return append(json.RawMessage(nil), raw...), metadata, nil
}

func metadataBool(metadata map[string]any, key string) (bool, bool) {
	if len(metadata) == 0 {
		return false, false
	}
	raw, ok := metadata[key]
	if !ok {
		return false, false
	}
	switch value := raw.(type) {
	case bool:
		return value, true
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on", "disabled":
			return true, true
		case "0", "false", "no", "off", "enabled":
			return false, true
		}
	case float64:
		return value != 0, true
	case int:
		return value != 0, true
	case int64:
		return value != 0, true
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return parsed != 0, true
		}
	}
	return false, false
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneAuthError(err *cliproxyauth.Error) *cliproxyauth.Error {
	if err == nil {
		return nil
	}
	return &cliproxyauth.Error{
		Code:       err.Code,
		Message:    err.Message,
		Retryable:  err.Retryable,
		HTTPStatus: err.HTTPStatus,
	}
}

func cloneModelStates(states map[string]*cliproxyauth.ModelState) map[string]*cliproxyauth.ModelState {
	if len(states) == 0 {
		return nil
	}
	out := make(map[string]*cliproxyauth.ModelState, len(states))
	for key, state := range states {
		if state != nil {
			out[key] = state.Clone()
		}
	}
	return out
}

func cloneLocalRateLimitState(state cliproxyauth.LocalRateLimitState) cliproxyauth.LocalRateLimitState {
	clone := state
	if len(state.RequestTimestamps) > 0 {
		clone.RequestTimestamps = append([]time.Time(nil), state.RequestTimestamps...)
	}
	return clone
}

func authAttributePath(auth *cliproxyauth.Auth) string {
	if auth == nil || auth.Attributes == nil {
		return ""
	}
	return strings.TrimSpace(auth.Attributes["path"])
}
