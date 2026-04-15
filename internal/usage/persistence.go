package usage

import (
	"context"
	"errors"
	"time"
)

const DefaultRetentionDays = 15

var ErrUsageRetentionUnsupported = errors.New("usage retention unsupported")

// PersistentRecord is the normalized usage row persisted by durable backends.
type PersistentRecord struct {
	APIName     string
	Provider    string
	Model       string
	AuthID      string
	AuthIndex   string
	Source      string
	RequestedAt time.Time
	LatencyMs   int64
	Failed      bool
	Tokens      TokenStats
}

// PersistentStore persists usage records and retention settings.
type PersistentStore interface {
	SaveUsageRecord(ctx context.Context, record PersistentRecord) error
	LoadUsageSnapshot(ctx context.Context) (StatisticsSnapshot, error)
	MergeUsageSnapshot(ctx context.Context, snapshot StatisticsSnapshot) (MergeResult, error)
	GetUsageRetentionDays(ctx context.Context) (int, error)
	SetUsageRetentionDays(ctx context.Context, days int) error
}

// NormalizeRetentionDays clamps invalid retention settings to the default.
func NormalizeRetentionDays(days int) int {
	if days <= 0 {
		return DefaultRetentionDays
	}
	return days
}

// PersistentDedupKey generates a stable deduplication key for durable usage rows.
func PersistentDedupKey(record PersistentRecord) string {
	return dedupKey(
		record.APIName,
		record.Model,
		RequestDetail{
			Timestamp: record.RequestedAt,
			LatencyMs: record.LatencyMs,
			Source:    record.Source,
			AuthIndex: record.AuthIndex,
			Tokens:    normaliseTokenStats(record.Tokens),
			Failed:    record.Failed,
		},
	)
}
