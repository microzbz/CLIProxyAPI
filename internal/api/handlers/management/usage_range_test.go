package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestGetUsageStatisticsFiltersRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stats := usage.NewRequestStatistics()
	now := time.Now().UTC()
	stats.Record(context.Background(), coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-test",
		APIKey:      "key",
		RequestedAt: now.Add(-30 * time.Minute),
		Detail: coreusage.Detail{
			TotalTokens: 10,
		},
	})
	stats.Record(context.Background(), coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-test",
		APIKey:      "key",
		RequestedAt: now.Add(-2 * time.Hour),
		Detail: coreusage.Detail{
			TotalTokens: 20,
		},
	})

	h := &Handler{usageStats: stats}
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage?range=1h", nil)

	h.GetUsageStatistics(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var payload struct {
		Usage usage.StatisticsSnapshot `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Usage.TotalRequests != 1 {
		t.Fatalf("total_requests = %d, want 1; body=%s", payload.Usage.TotalRequests, rec.Body.String())
	}
	if payload.Usage.TotalTokens != 10 {
		t.Fatalf("total_tokens = %d, want 10; body=%s", payload.Usage.TotalTokens, rec.Body.String())
	}
}

func TestGetUsageStatisticsRejectsInvalidRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &Handler{usageStats: usage.NewRequestStatistics()}
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage?range=forever", nil)

	h.GetUsageStatistics(ctx)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
