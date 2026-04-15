package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

type usageSettingsTestStore struct {
	retentionDays int
}

func (s *usageSettingsTestStore) SaveUsageRecord(context.Context, usage.PersistentRecord) error {
	return nil
}

func (s *usageSettingsTestStore) LoadUsageSnapshot(context.Context) (usage.StatisticsSnapshot, error) {
	return usage.StatisticsSnapshot{}, nil
}

func (s *usageSettingsTestStore) MergeUsageSnapshot(context.Context, usage.StatisticsSnapshot) (usage.MergeResult, error) {
	return usage.MergeResult{}, nil
}

func (s *usageSettingsTestStore) GetUsageRetentionDays(context.Context) (int, error) {
	return s.retentionDays, nil
}

func (s *usageSettingsTestStore) SetUsageRetentionDays(_ context.Context, days int) error {
	s.retentionDays = days
	return nil
}

func TestUsageRetentionDaysEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stats := usage.NewRequestStatistics()
	store := &usageSettingsTestStore{retentionDays: 15}
	stats.SetPersistentStore(store)

	h := NewHandler(&config.Config{}, "", nil)
	h.SetUsageStatistics(stats)

	getRecorder := httptest.NewRecorder()
	getCtx, _ := gin.CreateTestContext(getRecorder)
	req, _ := http.NewRequest(http.MethodGet, "/v0/management/usage-retention-days", nil)
	getCtx.Request = req

	h.GetUsageRetentionDays(getCtx)

	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getRecorder.Code)
	}
	var getBody map[string]any
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &getBody); err != nil {
		t.Fatalf("unmarshal get body: %v", err)
	}
	if got := int(getBody["usage-retention-days"].(float64)); got != 15 {
		t.Fatalf("usage-retention-days = %d, want 15", got)
	}
	if got := getBody["backend"].(string); got != "postgres" {
		t.Fatalf("backend = %q, want postgres", got)
	}

	putRecorder := httptest.NewRecorder()
	putCtx, _ := gin.CreateTestContext(putRecorder)
	reqBody := []byte(`{"value":30}`)
	putReq, _ := http.NewRequest(http.MethodPut, "/v0/management/usage-retention-days", bytes.NewReader(reqBody))
	putReq.Header.Set("Content-Type", "application/json")
	putCtx.Request = putReq

	h.PutUsageRetentionDays(putCtx)

	if putRecorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200, body=%s", putRecorder.Code, putRecorder.Body.String())
	}
	if store.retentionDays != 30 {
		t.Fatalf("retentionDays = %d, want 30", store.retentionDays)
	}
}
