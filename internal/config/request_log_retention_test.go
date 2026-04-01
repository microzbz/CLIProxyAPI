package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigOptionalDefaultsRequestLogRetentionDays(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("request-log: true\n"), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if cfg.RequestLogRetentionDays != DefaultRequestLogRetentionDays {
		t.Fatalf("expected request log retention default %d, got %d", DefaultRequestLogRetentionDays, cfg.RequestLogRetentionDays)
	}
}
