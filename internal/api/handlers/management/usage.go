package management

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

type usageExportPayload struct {
	Version    int                      `json:"version"`
	ExportedAt time.Time                `json:"exported_at"`
	Usage      usage.StatisticsSnapshot `json:"usage"`
}

type usageImportPayload struct {
	Version int                      `json:"version"`
	Usage   usage.StatisticsSnapshot `json:"usage"`
}

// GetUsageStatistics returns the in-memory request statistics snapshot.
func (h *Handler) GetUsageStatistics(c *gin.Context) {
	var snapshot usage.StatisticsSnapshot
	if h != nil && h.usageStats != nil {
		since, ranged, ok := parseUsageStatisticsRange(c)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "range must be all, 1h, 7h, 24h, 7d, hours, or an RFC3339/unix since value"})
			return
		}
		if ranged {
			snapshot = h.usageStats.SnapshotSince(since)
		} else {
			snapshot = h.usageStats.Snapshot()
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"usage":           snapshot,
		"failed_requests": snapshot.FailureCount,
	})
}

func parseUsageStatisticsRange(c *gin.Context) (time.Time, bool, bool) {
	if c == nil {
		return time.Time{}, false, true
	}
	if sinceRaw := strings.TrimSpace(c.Query("since")); sinceRaw != "" {
		since, ok := parseUsageSinceValue(sinceRaw)
		return since, ok, ok
	}
	if hoursRaw := strings.TrimSpace(c.Query("hours")); hoursRaw != "" {
		hours, err := strconv.ParseFloat(hoursRaw, 64)
		if err != nil || hours <= 0 {
			return time.Time{}, false, false
		}
		return time.Now().UTC().Add(-time.Duration(hours * float64(time.Hour))), true, true
	}
	value := strings.ToLower(strings.TrimSpace(c.Query("range")))
	if value == "" || value == "all" || value == "*" {
		return time.Time{}, false, true
	}
	duration, ok := map[string]time.Duration{
		"1h":  time.Hour,
		"7h":  7 * time.Hour,
		"24h": 24 * time.Hour,
		"1d":  24 * time.Hour,
		"7d":  7 * 24 * time.Hour,
	}[value]
	if !ok {
		return time.Time{}, false, false
	}
	return time.Now().UTC().Add(-duration), true, true
}

func parseUsageSinceValue(raw string) (time.Time, bool) {
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts.UTC(), true
	}
	unixValue, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	switch {
	case unixValue > 1_000_000_000_000:
		return time.UnixMilli(unixValue).UTC(), true
	case unixValue > 0:
		return time.Unix(unixValue, 0).UTC(), true
	default:
		return time.Time{}, false
	}
}

// ExportUsageStatistics returns a complete usage snapshot for backup/migration.
func (h *Handler) ExportUsageStatistics(c *gin.Context) {
	var snapshot usage.StatisticsSnapshot
	if h != nil && h.usageStats != nil {
		snapshot = h.usageStats.Snapshot()
	}
	c.JSON(http.StatusOK, usageExportPayload{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Usage:      snapshot,
	})
}

// ImportUsageStatistics merges a previously exported usage snapshot into memory.
func (h *Handler) ImportUsageStatistics(c *gin.Context) {
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usage statistics unavailable"})
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var payload usageImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if payload.Version != 0 && payload.Version != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported version"})
		return
	}

	result := h.usageStats.MergeSnapshot(payload.Usage)
	snapshot := h.usageStats.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"added":           result.Added,
		"skipped":         result.Skipped,
		"total_requests":  snapshot.TotalRequests,
		"failed_requests": snapshot.FailureCount,
	})
}

// GetUsageRetentionDays returns the effective usage retention setting.
func (h *Handler) GetUsageRetentionDays(c *gin.Context) {
	days := usage.DefaultRetentionDays
	backend := "memory"
	editable := false
	if h != nil && h.usageStats != nil {
		backend = h.usageStats.UsageBackend()
		editable = backend != "memory"
		if value, err := h.usageStats.GetUsageRetentionDays(c.Request.Context()); err == nil {
			days = value
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"usage-retention-days": days,
		"backend":              backend,
		"editable":             editable,
	})
}

// PutUsageRetentionDays updates the usage retention setting for durable backends.
func (h *Handler) PutUsageRetentionDays(c *gin.Context) {
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usage statistics unavailable"})
		return
	}
	var body struct {
		Value *int `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	days := usage.NormalizeRetentionDays(*body.Value)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.usageStats.SetUsageRetentionDays(ctx, days); err != nil {
		if errors.Is(err, usage.ErrUsageRetentionUnsupported) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "usage retention requires postgres store"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage-retention-days": days, "ok": true})
}
