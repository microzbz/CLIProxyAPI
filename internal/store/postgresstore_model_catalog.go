package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SaveModelsCatalog persists the latest fetched models catalog in PostgreSQL.
func (s *PostgresStore) SaveModelsCatalog(ctx context.Context, payload []byte, source string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres store: not initialized")
	}
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return fmt.Errorf("postgres store: empty models catalog payload")
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return fmt.Errorf("postgres store: decode models catalog payload: %w", err)
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (id, source, content, created_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (id)
		DO UPDATE SET source = EXCLUDED.source, content = EXCLUDED.content, updated_at = NOW()
	`, s.fullTableName(s.cfg.ModelsTable))
	if _, err := s.db.ExecContext(ctx, query, defaultModelsKey, strings.TrimSpace(source), raw); err != nil {
		return fmt.Errorf("postgres store: upsert models catalog: %w", err)
	}
	return nil
}

// LoadModelsCatalog returns the last persisted models catalog snapshot, if one exists.
func (s *PostgresStore) LoadModelsCatalog(ctx context.Context) ([]byte, string, time.Time, bool, error) {
	if s == nil || s.db == nil {
		return nil, "", time.Time{}, false, fmt.Errorf("postgres store: not initialized")
	}
	query := fmt.Sprintf(`
		SELECT content, source, updated_at
		FROM %s
		WHERE id = $1
	`, s.fullTableName(s.cfg.ModelsTable))
	var (
		payload   string
		source    string
		updatedAt time.Time
	)
	err := s.db.QueryRowContext(ctx, query, defaultModelsKey).Scan(&payload, &source, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, "", time.Time{}, false, nil
	case err != nil:
		return nil, "", time.Time{}, false, fmt.Errorf("postgres store: load models catalog: %w", err)
	}
	return []byte(payload), source, updatedAt, true, nil
}
