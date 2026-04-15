package usage

import (
	"context"
	"errors"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

type mockPersistentStore struct {
	record        PersistentRecord
	saveCalls     int
	snapshot      StatisticsSnapshot
	mergeResult   MergeResult
	retentionDays int
	setDays       int
}

func (m *mockPersistentStore) SaveUsageRecord(_ context.Context, record PersistentRecord) error {
	m.record = record
	m.saveCalls++
	return nil
}

func (m *mockPersistentStore) LoadUsageSnapshot(context.Context) (StatisticsSnapshot, error) {
	return m.snapshot, nil
}

func (m *mockPersistentStore) MergeUsageSnapshot(context.Context, StatisticsSnapshot) (MergeResult, error) {
	return m.mergeResult, nil
}

func (m *mockPersistentStore) GetUsageRetentionDays(context.Context) (int, error) {
	return m.retentionDays, nil
}

func (m *mockPersistentStore) SetUsageRetentionDays(_ context.Context, days int) error {
	m.setDays = days
	return nil
}

func TestRequestStatisticsRecordIncludesLatency(t *testing.T) {
	stats := NewRequestStatistics()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
		Latency:     1500 * time.Millisecond,
		Detail: coreusage.Detail{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	})

	snapshot := stats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	if details[0].LatencyMs != 1500 {
		t.Fatalf("latency_ms = %d, want 1500", details[0].LatencyMs)
	}
}

func TestRequestStatisticsMergeSnapshotDedupIgnoresLatency(t *testing.T) {
	stats := NewRequestStatistics()
	timestamp := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	first := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 0,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}
	second := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 2500,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}

	result := stats.MergeSnapshot(first)
	if result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("first merge = %+v, want added=1 skipped=0", result)
	}

	result = stats.MergeSnapshot(second)
	if result.Added != 0 || result.Skipped != 1 {
		t.Fatalf("second merge = %+v, want added=0 skipped=1", result)
	}

	snapshot := stats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
}

func TestRequestStatisticsRecordUsesPersistentStore(t *testing.T) {
	stats := NewRequestStatistics()
	store := &mockPersistentStore{}
	stats.SetPersistentStore(store)

	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "test-key",
		Provider:    "codex",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
		Latency:     1200 * time.Millisecond,
		Detail: coreusage.Detail{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	})

	if store.saveCalls != 1 {
		t.Fatalf("saveCalls = %d, want 1", store.saveCalls)
	}
	if store.record.APIName != "test-key" {
		t.Fatalf("apiName = %q, want test-key", store.record.APIName)
	}
	if store.record.LatencyMs != 1200 {
		t.Fatalf("latencyMs = %d, want 1200", store.record.LatencyMs)
	}
	if snapshot := stats.Snapshot(); snapshot.TotalRequests != 0 {
		t.Fatalf("memory snapshot total_requests = %d, want 0 when using persistent store", snapshot.TotalRequests)
	}
}

func TestRequestStatisticsRetentionUsesPersistentStore(t *testing.T) {
	stats := NewRequestStatistics()
	store := &mockPersistentStore{retentionDays: 21}
	stats.SetPersistentStore(store)

	days, err := stats.GetUsageRetentionDays(context.Background())
	if err != nil {
		t.Fatalf("GetUsageRetentionDays() error = %v", err)
	}
	if days != 21 {
		t.Fatalf("days = %d, want 21", days)
	}

	if err := stats.SetUsageRetentionDays(context.Background(), 0); err != nil {
		t.Fatalf("SetUsageRetentionDays() error = %v", err)
	}
	if store.setDays != DefaultRetentionDays {
		t.Fatalf("setDays = %d, want %d", store.setDays, DefaultRetentionDays)
	}
}

func TestRequestStatisticsRetentionUnsupportedWithoutPersistentStore(t *testing.T) {
	stats := NewRequestStatistics()

	days, err := stats.GetUsageRetentionDays(context.Background())
	if err != nil {
		t.Fatalf("GetUsageRetentionDays() error = %v", err)
	}
	if days != DefaultRetentionDays {
		t.Fatalf("days = %d, want %d", days, DefaultRetentionDays)
	}
	if err := stats.SetUsageRetentionDays(context.Background(), 7); !errors.Is(err, ErrUsageRetentionUnsupported) {
		t.Fatalf("SetUsageRetentionDays() error = %v, want ErrUsageRetentionUnsupported", err)
	}
}
