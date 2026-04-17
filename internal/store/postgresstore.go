package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	defaultConfigTable   = "config_store"
	defaultAuthTable     = "auth_store"
	defaultModelsTable   = "model_catalog_store"
	defaultSettingsTable = "settings_store"
	defaultUsageTable    = "usage_store"
	defaultConfigKey     = "config"
	defaultModelsKey     = "default"
	usageRetentionKey    = "usage_retention_days"
)

// PostgresStoreConfig captures configuration required to initialize a Postgres-backed store.
type PostgresStoreConfig struct {
	DSN           string
	Schema        string
	ConfigTable   string
	AuthTable     string
	ModelsTable   string
	SettingsTable string
	UsageTable    string
	SpoolDir      string
}

// PostgresStore persists configuration and authentication metadata using PostgreSQL as backend
// while mirroring data to a local workspace so existing file-based workflows continue to operate.
type PostgresStore struct {
	db               *sql.DB
	cfg              PostgresStoreConfig
	spoolRoot        string
	configPath       string
	authDir          string
	mu               sync.Mutex
	usageCleanupMu   sync.Mutex
	lastUsageCleanup time.Time
}

// NewPostgresStore establishes a connection to PostgreSQL and prepares the local workspace.
func NewPostgresStore(ctx context.Context, cfg PostgresStoreConfig) (*PostgresStore, error) {
	trimmedDSN := strings.TrimSpace(cfg.DSN)
	if trimmedDSN == "" {
		return nil, fmt.Errorf("postgres store: DSN is required")
	}
	cfg.DSN = trimmedDSN
	if cfg.ConfigTable == "" {
		cfg.ConfigTable = defaultConfigTable
	}
	if cfg.AuthTable == "" {
		cfg.AuthTable = defaultAuthTable
	}
	if cfg.ModelsTable == "" {
		cfg.ModelsTable = defaultModelsTable
	}
	if cfg.SettingsTable == "" {
		cfg.SettingsTable = defaultSettingsTable
	}
	if cfg.UsageTable == "" {
		cfg.UsageTable = defaultUsageTable
	}

	spoolRoot := strings.TrimSpace(cfg.SpoolDir)
	if spoolRoot == "" {
		if cwd, err := os.Getwd(); err == nil {
			spoolRoot = filepath.Join(cwd, "pgstore")
		} else {
			spoolRoot = filepath.Join(os.TempDir(), "pgstore")
		}
	}
	absSpool, err := filepath.Abs(spoolRoot)
	if err != nil {
		return nil, fmt.Errorf("postgres store: resolve spool directory: %w", err)
	}
	configDir := filepath.Join(absSpool, "config")
	authDir := filepath.Join(absSpool, "auths")
	if err = os.MkdirAll(configDir, 0o700); err != nil {
		return nil, fmt.Errorf("postgres store: create config directory: %w", err)
	}
	if err = os.MkdirAll(authDir, 0o700); err != nil {
		return nil, fmt.Errorf("postgres store: create auth directory: %w", err)
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres store: open database connection: %w", err)
	}
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres store: ping database: %w", err)
	}

	store := &PostgresStore{
		db:         db,
		cfg:        cfg,
		spoolRoot:  absSpool,
		configPath: filepath.Join(configDir, "config.yaml"),
		authDir:    authDir,
	}
	return store, nil
}

// Close releases the underlying database connection.
func (s *PostgresStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// EnsureSchema creates the required tables (and schema when provided).
func (s *PostgresStore) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres store: not initialized")
	}
	if schema := strings.TrimSpace(s.cfg.Schema); schema != "" {
		query := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quoteIdentifier(schema))
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("postgres store: create schema: %w", err)
		}
	}
	configTable := s.fullTableName(s.cfg.ConfigTable)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`, configTable)); err != nil {
		return fmt.Errorf("postgres store: create config table: %w", err)
	}
	authTable := s.fullTableName(s.cfg.AuthTable)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				content JSONB NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)
		`, authTable)); err != nil {
		return fmt.Errorf("postgres store: create auth table: %w", err)
	}
	modelsTable := s.fullTableName(s.cfg.ModelsTable)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				source TEXT NOT NULL DEFAULT '',
				content JSONB NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)
		`, modelsTable)); err != nil {
		return fmt.Errorf("postgres store: create model catalog table: %w", err)
	}
	settingsTable := s.fullTableName(s.cfg.SettingsTable)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`, settingsTable)); err != nil {
		return fmt.Errorf("postgres store: create settings table: %w", err)
	}
	usageTable := s.fullTableName(s.cfg.UsageTable)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id BIGSERIAL PRIMARY KEY,
			dedup_key TEXT NOT NULL UNIQUE,
			api_name TEXT NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			auth_id TEXT NOT NULL DEFAULT '',
			auth_index TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			requested_at TIMESTAMPTZ NOT NULL,
			latency_ms BIGINT NOT NULL DEFAULT 0,
			failed BOOLEAN NOT NULL DEFAULT FALSE,
			input_tokens BIGINT NOT NULL DEFAULT 0,
			output_tokens BIGINT NOT NULL DEFAULT 0,
			reasoning_tokens BIGINT NOT NULL DEFAULT 0,
			cached_tokens BIGINT NOT NULL DEFAULT 0,
			total_tokens BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`, usageTable)); err != nil {
		return fmt.Errorf("postgres store: create usage table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (requested_at DESC)`, quoteIdentifier(s.cfg.UsageTable+"_requested_at_idx"), usageTable)); err != nil {
		return fmt.Errorf("postgres store: create usage requested_at index: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (api_name, model)`, quoteIdentifier(s.cfg.UsageTable+"_api_model_idx"), usageTable)); err != nil {
		return fmt.Errorf("postgres store: create usage api/model index: %w", err)
	}
	if err := s.ensureDefaultUsageRetention(ctx); err != nil {
		return err
	}
	return nil
}

func (s *PostgresStore) ensureDefaultUsageRetention(ctx context.Context) error {
	days := usage.DefaultRetentionDays
	query := fmt.Sprintf(`
		INSERT INTO %s (key, value, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		ON CONFLICT (key) DO NOTHING
	`, s.fullTableName(s.cfg.SettingsTable))
	if _, err := s.db.ExecContext(ctx, query, usageRetentionKey, fmt.Sprintf("%d", days)); err != nil {
		return fmt.Errorf("postgres store: seed usage retention setting: %w", err)
	}
	return nil
}

// Bootstrap synchronizes auth records between PostgreSQL and the local workspace.
// Configuration is intentionally file-only and is no longer loaded from or persisted to PostgreSQL.
func (s *PostgresStore) Bootstrap(ctx context.Context, exampleConfigPath string) error {
	_ = exampleConfigPath
	if err := s.EnsureSchema(ctx); err != nil {
		return err
	}
	if err := s.syncAuthFromDatabase(ctx); err != nil {
		return err
	}
	return nil
}

// ConfigPath returns the managed configuration file path inside the spool directory.
func (s *PostgresStore) ConfigPath() string {
	if s == nil {
		return ""
	}
	return s.configPath
}

// AuthDir returns the local directory containing mirrored auth files.
func (s *PostgresStore) AuthDir() string {
	if s == nil {
		return ""
	}
	return s.authDir
}

// UseStoreAuthSource marks PostgreSQL as the authoritative source for auth-file state.
func (s *PostgresStore) UseStoreAuthSource() bool {
	return s != nil
}

// WorkDir exposes the root spool directory used for mirroring.
func (s *PostgresStore) WorkDir() string {
	if s == nil {
		return ""
	}
	return s.spoolRoot
}

// SetBaseDir implements the optional interface used by authenticators; it is a no-op because
// the Postgres-backed store controls its own workspace.
func (s *PostgresStore) SetBaseDir(string) {}

// Save persists authentication metadata to disk and PostgreSQL.
func (s *PostgresStore) Save(ctx context.Context, auth *cliproxyauth.Auth) (string, error) {
	if auth == nil {
		return "", fmt.Errorf("postgres store: auth is nil")
	}

	path, err := s.resolveAuthPath(auth)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("postgres store: missing file path attribute for %s", auth.ID)
	}

	if auth.Disabled {
		if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
			return "", nil
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("postgres store: create auth directory: %w", err)
	}

	type metadataSetter interface {
		SetMetadata(map[string]any)
	}

	var metadataPayload []byte
	switch {
	case auth.Storage != nil:
		if setter, ok := auth.Storage.(metadataSetter); ok {
			setter.SetMetadata(auth.Metadata)
		}
		if err = auth.Storage.SaveTokenToFile(path); err != nil {
			return "", err
		}
		metadataPayload, err = os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("postgres store: read persisted auth file: %w", err)
		}
	case auth.Metadata != nil:
		auth.Metadata["disabled"] = auth.Disabled
		raw, errMarshal := json.Marshal(auth.Metadata)
		if errMarshal != nil {
			return "", fmt.Errorf("postgres store: marshal metadata: %w", errMarshal)
		}
		metadataPayload = raw
		shouldWrite := true
		if existing, errRead := os.ReadFile(path); errRead == nil {
			if jsonEqual(existing, raw) {
				shouldWrite = false
			}
		} else if errRead != nil && !errors.Is(errRead, fs.ErrNotExist) {
			return "", fmt.Errorf("postgres store: read existing metadata: %w", errRead)
		}
		if shouldWrite {
			tmp := path + ".tmp"
			if errWrite := os.WriteFile(tmp, raw, 0o600); errWrite != nil {
				return "", fmt.Errorf("postgres store: write temp auth file: %w", errWrite)
			}
			if errRename := os.Rename(tmp, path); errRename != nil {
				return "", fmt.Errorf("postgres store: rename auth file: %w", errRename)
			}
		}
	default:
		return "", fmt.Errorf("postgres store: nothing to persist for %s", auth.ID)
	}

	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	auth.Attributes["path"] = path

	if strings.TrimSpace(auth.FileName) == "" {
		auth.FileName = auth.ID
	}

	relID, err := s.relativeAuthID(path)
	if err != nil {
		return "", err
	}
	if err = s.upsertAuthRecord(ctx, relID, path, auth, metadataPayload); err != nil {
		return "", err
	}
	return path, nil
}

// List enumerates all auth records stored in PostgreSQL.
func (s *PostgresStore) List(ctx context.Context) ([]*cliproxyauth.Auth, error) {
	query := fmt.Sprintf("SELECT id, content, created_at, updated_at FROM %s ORDER BY id", s.fullTableName(s.cfg.AuthTable))
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list auth: %w", err)
	}
	defer rows.Close()

	auths := make([]*cliproxyauth.Auth, 0, 32)
	for rows.Next() {
		var (
			id        string
			payload   string
			createdAt time.Time
			updatedAt time.Time
		)
		if err = rows.Scan(&id, &payload, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("postgres store: scan auth row: %w", err)
		}
		path, errPath := s.absoluteAuthPath(id)
		if errPath != nil {
			log.WithError(errPath).Warnf("postgres store: skipping auth %s outside spool", id)
			continue
		}
		auth, _, errDecode := decodePersistedAuthPayload(id, []byte(payload), createdAt, updatedAt, path)
		if errDecode != nil {
			log.WithError(errDecode).Warnf("postgres store: skipping auth %s with invalid json", id)
			continue
		}
		auths = append(auths, auth)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: iterate auth rows: %w", err)
	}
	return auths, nil
}

// Delete removes an auth file and the corresponding database record.
func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("postgres store: id is empty")
	}
	path, err := s.resolveDeletePath(id)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err = os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("postgres store: delete auth file: %w", err)
	}
	relID, err := s.relativeAuthID(path)
	if err != nil {
		return err
	}
	return s.deleteAuthRecord(ctx, relID)
}

// PersistAuthFiles stores the provided auth file changes in PostgreSQL.
func (s *PostgresStore) PersistAuthFiles(ctx context.Context, _ string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range paths {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		relID, err := s.relativeAuthID(trimmed)
		if err != nil {
			// Attempt to resolve absolute path under authDir.
			abs := trimmed
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(s.authDir, trimmed)
			}
			relID, err = s.relativeAuthID(abs)
			if err != nil {
				log.WithError(err).Warnf("postgres store: ignoring auth path %s", trimmed)
				continue
			}
			trimmed = abs
		}
		if err = s.syncAuthFile(ctx, relID, trimmed); err != nil {
			return err
		}
	}
	return nil
}

// PersistConfig is intentionally a no-op: configuration now stays file-backed even when
// PostgreSQL is enabled for auth mirroring and usage persistence.
func (s *PostgresStore) PersistConfig(ctx context.Context) error {
	_ = ctx
	return nil
}

// syncConfigFromDatabase writes the database-stored config to disk or seeds the database from template.
func (s *PostgresStore) syncConfigFromDatabase(ctx context.Context, exampleConfigPath string) error {
	query := fmt.Sprintf("SELECT content FROM %s WHERE id = $1", s.fullTableName(s.cfg.ConfigTable))
	var content string
	err := s.db.QueryRowContext(ctx, query, defaultConfigKey).Scan(&content)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, errStat := os.Stat(s.configPath); errors.Is(errStat, fs.ErrNotExist) {
			if exampleConfigPath != "" {
				if errCopy := misc.CopyConfigTemplate(exampleConfigPath, s.configPath); errCopy != nil {
					return fmt.Errorf("postgres store: copy example config: %w", errCopy)
				}
			} else {
				if errCreate := os.MkdirAll(filepath.Dir(s.configPath), 0o700); errCreate != nil {
					return fmt.Errorf("postgres store: prepare config directory: %w", errCreate)
				}
				if errWrite := os.WriteFile(s.configPath, []byte{}, 0o600); errWrite != nil {
					return fmt.Errorf("postgres store: create empty config: %w", errWrite)
				}
			}
		}
		data, errRead := os.ReadFile(s.configPath)
		if errRead != nil {
			return fmt.Errorf("postgres store: read local config: %w", errRead)
		}
		if errPersist := s.persistConfig(ctx, data); errPersist != nil {
			return errPersist
		}
	case err != nil:
		return fmt.Errorf("postgres store: load config from database: %w", err)
	default:
		if err = os.MkdirAll(filepath.Dir(s.configPath), 0o700); err != nil {
			return fmt.Errorf("postgres store: prepare config directory: %w", err)
		}
		normalized := normalizeLineEndings(content)
		if err = os.WriteFile(s.configPath, []byte(normalized), 0o600); err != nil {
			return fmt.Errorf("postgres store: write config to spool: %w", err)
		}
	}
	return nil
}

// syncAuthFromDatabase populates the local auth directory from PostgreSQL data.
func (s *PostgresStore) syncAuthFromDatabase(ctx context.Context) error {
	query := fmt.Sprintf("SELECT id, content FROM %s", s.fullTableName(s.cfg.AuthTable))
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("postgres store: load auth from database: %w", err)
	}
	defer rows.Close()

	if err = os.RemoveAll(s.authDir); err != nil {
		return fmt.Errorf("postgres store: reset auth directory: %w", err)
	}
	if err = os.MkdirAll(s.authDir, 0o700); err != nil {
		return fmt.Errorf("postgres store: recreate auth directory: %w", err)
	}

	for rows.Next() {
		var (
			id      string
			payload string
		)
		if err = rows.Scan(&id, &payload); err != nil {
			return fmt.Errorf("postgres store: scan auth row: %w", err)
		}
		path, errPath := s.absoluteAuthPath(id)
		if errPath != nil {
			log.WithError(errPath).Warnf("postgres store: skipping auth %s outside spool", id)
			continue
		}
		_, metadataPayload, errDecode := decodePersistedAuthPayload(id, []byte(payload), time.Time{}, time.Time{}, path)
		if errDecode != nil {
			return fmt.Errorf("postgres store: decode auth %s: %w", id, errDecode)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("postgres store: create auth subdir: %w", err)
		}
		if err = os.WriteFile(path, metadataPayload, 0o600); err != nil {
			return fmt.Errorf("postgres store: write auth file: %w", err)
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("postgres store: iterate auth rows: %w", err)
	}
	return nil
}

func (s *PostgresStore) syncAuthFile(ctx context.Context, relID, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s.deleteAuthRecord(ctx, relID)
		}
		return fmt.Errorf("postgres store: read auth file: %w", err)
	}
	if len(data) == 0 {
		return s.deleteAuthRecord(ctx, relID)
	}
	return s.persistAuth(ctx, relID, data, nil)
}

func (s *PostgresStore) upsertAuthRecord(ctx context.Context, relID, path string, auth *cliproxyauth.Auth, data []byte) error {
	var err error
	if len(data) == 0 {
		data, err = os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("postgres store: read auth file: %w", err)
		}
	}
	if len(data) == 0 {
		return s.deleteAuthRecord(ctx, relID)
	}
	return s.persistAuth(ctx, relID, data, auth)
}

func (s *PostgresStore) persistAuth(ctx context.Context, relID string, data []byte, auth *cliproxyauth.Auth) error {
	jsonPayload, err := s.buildPersistedAuthPayload(ctx, relID, data, auth)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (id, content, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		ON CONFLICT (id)
		DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()
	`, s.fullTableName(s.cfg.AuthTable))
	if _, err := s.db.ExecContext(ctx, query, relID, json.RawMessage(jsonPayload)); err != nil {
		return fmt.Errorf("postgres store: upsert auth record: %w", err)
	}
	return nil
}

func (s *PostgresStore) deleteAuthRecord(ctx context.Context, relID string) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1", s.fullTableName(s.cfg.AuthTable))
	if _, err := s.db.ExecContext(ctx, query, relID); err != nil {
		return fmt.Errorf("postgres store: delete auth record: %w", err)
	}
	return nil
}

func (s *PostgresStore) persistConfig(ctx context.Context, data []byte) error {
	query := fmt.Sprintf(`
		INSERT INTO %s (id, content, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		ON CONFLICT (id)
		DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()
	`, s.fullTableName(s.cfg.ConfigTable))
	normalized := normalizeLineEndings(string(data))
	if _, err := s.db.ExecContext(ctx, query, defaultConfigKey, normalized); err != nil {
		return fmt.Errorf("postgres store: upsert config: %w", err)
	}
	return nil
}

func (s *PostgresStore) deleteConfigRecord(ctx context.Context) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1", s.fullTableName(s.cfg.ConfigTable))
	if _, err := s.db.ExecContext(ctx, query, defaultConfigKey); err != nil {
		return fmt.Errorf("postgres store: delete config: %w", err)
	}
	return nil
}

// SaveUsageRecord persists one usage event and opportunistically prunes expired rows.
func (s *PostgresStore) SaveUsageRecord(ctx context.Context, record usage.PersistentRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres store: not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	record.Tokens = normalizeUsageTokens(record.Tokens)
	record.RequestedAt = normalizeUsageTimestamp(record.RequestedAt)
	record.Model = normalizeUsageString(record.Model, "unknown")
	record.Provider = normalizeUsageString(record.Provider, "unknown")
	record.APIName = normalizeUsageString(record.APIName, "unknown")
	record.Source = strings.TrimSpace(record.Source)
	record.AuthID = strings.TrimSpace(record.AuthID)
	record.AuthIndex = strings.TrimSpace(record.AuthIndex)
	if record.LatencyMs < 0 {
		record.LatencyMs = 0
	}

	if err := s.maybeCleanupUsageRecords(ctx); err != nil {
		log.WithError(err).Warn("postgres store: usage cleanup failed")
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (
			dedup_key, api_name, provider, model, auth_id, auth_index, source,
			requested_at, latency_ms, failed,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, NOW())
		ON CONFLICT (dedup_key) DO NOTHING
	`, s.fullTableName(s.cfg.UsageTable))
	if _, err := s.db.ExecContext(
		ctx,
		query,
		usage.PersistentDedupKey(record),
		record.APIName,
		record.Provider,
		record.Model,
		record.AuthID,
		record.AuthIndex,
		record.Source,
		record.RequestedAt.UTC(),
		record.LatencyMs,
		record.Failed,
		record.Tokens.InputTokens,
		record.Tokens.OutputTokens,
		record.Tokens.ReasoningTokens,
		record.Tokens.CachedTokens,
		record.Tokens.TotalTokens,
	); err != nil {
		return fmt.Errorf("postgres store: insert usage record: %w", err)
	}
	return nil
}

// LoadUsageSnapshot aggregates retained usage rows into the management snapshot shape.
func (s *PostgresStore) LoadUsageSnapshot(ctx context.Context) (usage.StatisticsSnapshot, error) {
	if s == nil || s.db == nil {
		return usage.StatisticsSnapshot{}, fmt.Errorf("postgres store: not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.maybeCleanupUsageRecords(ctx); err != nil {
		log.WithError(err).Warn("postgres store: usage cleanup failed")
	}
	retentionDays, err := s.GetUsageRetentionDays(ctx)
	if err != nil {
		return usage.StatisticsSnapshot{}, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -usage.NormalizeRetentionDays(retentionDays))
	query := fmt.Sprintf(`
		SELECT api_name, model, requested_at, latency_ms, source, auth_index, failed,
		       input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens
		FROM %s
		WHERE requested_at >= $1
		ORDER BY requested_at ASC
	`, s.fullTableName(s.cfg.UsageTable))
	rows, err := s.db.QueryContext(ctx, query, cutoff)
	if err != nil {
		return usage.StatisticsSnapshot{}, fmt.Errorf("postgres store: query usage rows: %w", err)
	}
	defer rows.Close()

	snapshot := usage.StatisticsSnapshot{
		APIs:           make(map[string]usage.APISnapshot),
		RequestsByDay:  make(map[string]int64),
		RequestsByHour: make(map[string]int64),
		TokensByDay:    make(map[string]int64),
		TokensByHour:   make(map[string]int64),
	}
	for rows.Next() {
		var (
			apiName         string
			modelName       string
			requestedAt     time.Time
			latencyMs       int64
			source          string
			authIndex       string
			failed          bool
			inputTokens     int64
			outputTokens    int64
			reasoningTokens int64
			cachedTokens    int64
			totalTokens     int64
		)
		if err := rows.Scan(
			&apiName,
			&modelName,
			&requestedAt,
			&latencyMs,
			&source,
			&authIndex,
			&failed,
			&inputTokens,
			&outputTokens,
			&reasoningTokens,
			&cachedTokens,
			&totalTokens,
		); err != nil {
			return usage.StatisticsSnapshot{}, fmt.Errorf("postgres store: scan usage row: %w", err)
		}
		detail := usage.RequestDetail{
			Timestamp: requestedAt.UTC(),
			LatencyMs: latencyMs,
			Source:    source,
			AuthIndex: authIndex,
			Failed:    failed,
			Tokens: usage.TokenStats{
				InputTokens:     inputTokens,
				OutputTokens:    outputTokens,
				ReasoningTokens: reasoningTokens,
				CachedTokens:    cachedTokens,
				TotalTokens:     totalTokens,
			},
		}
		snapshot = appendUsageSnapshotRow(snapshot, apiName, modelName, detail)
	}
	if err := rows.Err(); err != nil {
		return usage.StatisticsSnapshot{}, fmt.Errorf("postgres store: iterate usage rows: %w", err)
	}
	return snapshot, nil
}

// MergeUsageSnapshot imports exported usage details into PostgreSQL while deduplicating.
func (s *PostgresStore) MergeUsageSnapshot(ctx context.Context, snapshot usage.StatisticsSnapshot) (usage.MergeResult, error) {
	if s == nil || s.db == nil {
		return usage.MergeResult{}, fmt.Errorf("postgres store: not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.maybeCleanupUsageRecords(ctx); err != nil {
		log.WithError(err).Warn("postgres store: usage cleanup failed")
	}
	retentionDays, err := s.GetUsageRetentionDays(ctx)
	if err != nil {
		return usage.MergeResult{}, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -usage.NormalizeRetentionDays(retentionDays))
	query := fmt.Sprintf(`
		INSERT INTO %s (
			dedup_key, api_name, provider, model, auth_id, auth_index, source,
			requested_at, latency_ms, failed,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, created_at
		)
		VALUES ($1, $2, '', $3, '', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW())
		ON CONFLICT (dedup_key) DO NOTHING
	`, s.fullTableName(s.cfg.UsageTable))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return usage.MergeResult{}, fmt.Errorf("postgres store: begin usage import tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return usage.MergeResult{}, fmt.Errorf("postgres store: prepare usage import: %w", err)
	}
	defer stmt.Close()

	result := usage.MergeResult{}
	for apiName, apiSnapshot := range snapshot.APIs {
		apiName = normalizeUsageString(apiName, "unknown")
		for modelName, modelSnapshot := range apiSnapshot.Models {
			modelName = normalizeUsageString(modelName, "unknown")
			for _, detail := range modelSnapshot.Details {
				detail.Tokens = normalizeUsageTokens(detail.Tokens)
				detail.Timestamp = normalizeUsageTimestamp(detail.Timestamp)
				if detail.Timestamp.Before(cutoff) {
					result.Skipped++
					continue
				}
				record := usage.PersistentRecord{
					APIName:     apiName,
					Model:       modelName,
					AuthIndex:   strings.TrimSpace(detail.AuthIndex),
					Source:      strings.TrimSpace(detail.Source),
					RequestedAt: detail.Timestamp.UTC(),
					LatencyMs:   detail.LatencyMs,
					Failed:      detail.Failed,
					Tokens:      detail.Tokens,
				}
				res, errExec := stmt.ExecContext(
					ctx,
					usage.PersistentDedupKey(record),
					record.APIName,
					record.Model,
					record.AuthIndex,
					record.Source,
					record.RequestedAt,
					record.LatencyMs,
					record.Failed,
					record.Tokens.InputTokens,
					record.Tokens.OutputTokens,
					record.Tokens.ReasoningTokens,
					record.Tokens.CachedTokens,
					record.Tokens.TotalTokens,
				)
				if errExec != nil {
					return usage.MergeResult{}, fmt.Errorf("postgres store: import usage row: %w", errExec)
				}
				affected, _ := res.RowsAffected()
				if affected > 0 {
					result.Added += affected
				} else {
					result.Skipped++
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return usage.MergeResult{}, fmt.Errorf("postgres store: commit usage import: %w", err)
	}
	return result, nil
}

// GetUsageRetentionDays returns the retained usage window.
func (s *PostgresStore) GetUsageRetentionDays(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return usage.DefaultRetentionDays, fmt.Errorf("postgres store: not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureDefaultUsageRetention(ctx); err != nil {
		return usage.DefaultRetentionDays, err
	}
	query := fmt.Sprintf("SELECT value FROM %s WHERE key = $1", s.fullTableName(s.cfg.SettingsTable))
	var raw string
	if err := s.db.QueryRowContext(ctx, query, usageRetentionKey).Scan(&raw); err != nil {
		return usage.DefaultRetentionDays, fmt.Errorf("postgres store: load usage retention: %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return usage.DefaultRetentionDays, nil
	}
	var days int
	if _, err := fmt.Sscanf(raw, "%d", &days); err != nil {
		return usage.DefaultRetentionDays, nil
	}
	return usage.NormalizeRetentionDays(days), nil
}

// SetUsageRetentionDays updates the retained usage window.
func (s *PostgresStore) SetUsageRetentionDays(ctx context.Context, days int) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres store: not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	days = usage.NormalizeRetentionDays(days)
	query := fmt.Sprintf(`
		INSERT INTO %s (key, value, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		ON CONFLICT (key)
		DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, s.fullTableName(s.cfg.SettingsTable))
	if _, err := s.db.ExecContext(ctx, query, usageRetentionKey, fmt.Sprintf("%d", days)); err != nil {
		return fmt.Errorf("postgres store: update usage retention: %w", err)
	}
	return s.cleanupUsageBefore(ctx, time.Now().UTC().AddDate(0, 0, -days))
}

func (s *PostgresStore) maybeCleanupUsageRecords(ctx context.Context) error {
	s.usageCleanupMu.Lock()
	defer s.usageCleanupMu.Unlock()
	now := time.Now().UTC()
	if !s.lastUsageCleanup.IsZero() && now.Sub(s.lastUsageCleanup) < time.Hour {
		return nil
	}
	days, err := s.GetUsageRetentionDays(ctx)
	if err != nil {
		return err
	}
	if err := s.cleanupUsageBefore(ctx, now.AddDate(0, 0, -usage.NormalizeRetentionDays(days))); err != nil {
		return err
	}
	s.lastUsageCleanup = now
	return nil
}

func (s *PostgresStore) cleanupUsageBefore(ctx context.Context, cutoff time.Time) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE requested_at < $1", s.fullTableName(s.cfg.UsageTable))
	if _, err := s.db.ExecContext(ctx, query, cutoff.UTC()); err != nil {
		return fmt.Errorf("postgres store: delete expired usage rows: %w", err)
	}
	return nil
}

func appendUsageSnapshotRow(snapshot usage.StatisticsSnapshot, apiName, modelName string, detail usage.RequestDetail) usage.StatisticsSnapshot {
	if snapshot.APIs == nil {
		snapshot.APIs = make(map[string]usage.APISnapshot)
	}
	if snapshot.RequestsByDay == nil {
		snapshot.RequestsByDay = make(map[string]int64)
	}
	if snapshot.RequestsByHour == nil {
		snapshot.RequestsByHour = make(map[string]int64)
	}
	if snapshot.TokensByDay == nil {
		snapshot.TokensByDay = make(map[string]int64)
	}
	if snapshot.TokensByHour == nil {
		snapshot.TokensByHour = make(map[string]int64)
	}

	totalTokens := normalizeUsageTokens(detail.Tokens).TotalTokens
	snapshot.TotalRequests++
	if detail.Failed {
		snapshot.FailureCount++
	} else {
		snapshot.SuccessCount++
	}
	snapshot.TotalTokens += totalTokens

	apiSnapshot := snapshot.APIs[apiName]
	if apiSnapshot.Models == nil {
		apiSnapshot.Models = make(map[string]usage.ModelSnapshot)
	}
	apiSnapshot.TotalRequests++
	apiSnapshot.TotalTokens += totalTokens

	modelSnapshot := apiSnapshot.Models[modelName]
	modelSnapshot.TotalRequests++
	modelSnapshot.TotalTokens += totalTokens
	modelSnapshot.Details = append(modelSnapshot.Details, detail)
	apiSnapshot.Models[modelName] = modelSnapshot
	snapshot.APIs[apiName] = apiSnapshot

	dayKey := detail.Timestamp.Format("2006-01-02")
	hourKey := fmt.Sprintf("%02d", detail.Timestamp.Hour())
	snapshot.RequestsByDay[dayKey]++
	snapshot.RequestsByHour[hourKey]++
	snapshot.TokensByDay[dayKey] += totalTokens
	snapshot.TokensByHour[hourKey] += totalTokens
	return snapshot
}

func normalizeUsageTokens(tokens usage.TokenStats) usage.TokenStats {
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = tokens.InputTokens + tokens.OutputTokens + tokens.ReasoningTokens
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = tokens.InputTokens + tokens.OutputTokens + tokens.ReasoningTokens + tokens.CachedTokens
	}
	return tokens
}

func normalizeUsageTimestamp(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Now().UTC()
	}
	return ts.UTC()
}

func normalizeUsageString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func (s *PostgresStore) resolveAuthPath(auth *cliproxyauth.Auth) (string, error) {
	if auth == nil {
		return "", fmt.Errorf("postgres store: auth is nil")
	}
	if auth.Attributes != nil {
		if p := strings.TrimSpace(auth.Attributes["path"]); p != "" {
			return p, nil
		}
	}
	if fileName := strings.TrimSpace(auth.FileName); fileName != "" {
		if filepath.IsAbs(fileName) {
			return fileName, nil
		}
		return filepath.Join(s.authDir, fileName), nil
	}
	if auth.ID == "" {
		return "", fmt.Errorf("postgres store: missing id")
	}
	if filepath.IsAbs(auth.ID) {
		return auth.ID, nil
	}
	return filepath.Join(s.authDir, filepath.FromSlash(auth.ID)), nil
}

func (s *PostgresStore) resolveDeletePath(id string) (string, error) {
	if strings.ContainsRune(id, os.PathSeparator) || filepath.IsAbs(id) {
		return id, nil
	}
	return filepath.Join(s.authDir, filepath.FromSlash(id)), nil
}

func (s *PostgresStore) relativeAuthID(path string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("postgres store: store not initialized")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.authDir, path)
	}
	clean := filepath.Clean(path)
	rel, err := filepath.Rel(s.authDir, clean)
	if err != nil {
		return "", fmt.Errorf("postgres store: compute relative path: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("postgres store: path %s outside managed directory", path)
	}
	return filepath.ToSlash(rel), nil
}

func (s *PostgresStore) absoluteAuthPath(id string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("postgres store: store not initialized")
	}
	clean := filepath.Clean(filepath.FromSlash(id))
	if strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("postgres store: invalid auth identifier %s", id)
	}
	path := filepath.Join(s.authDir, clean)
	rel, err := filepath.Rel(s.authDir, path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("postgres store: resolved auth path escapes auth directory")
	}
	return path, nil
}

func (s *PostgresStore) fullTableName(name string) string {
	if strings.TrimSpace(s.cfg.Schema) == "" {
		return quoteIdentifier(name)
	}
	return quoteIdentifier(s.cfg.Schema) + "." + quoteIdentifier(name)
}

func quoteIdentifier(identifier string) string {
	replaced := strings.ReplaceAll(identifier, "\"", "\"\"")
	return "\"" + replaced + "\""
}

func valueAsString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return ""
	}
}

func labelFor(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if v := strings.TrimSpace(valueAsString(metadata["label"])); v != "" {
		return v
	}
	if v := strings.TrimSpace(valueAsString(metadata["email"])); v != "" {
		return v
	}
	if v := strings.TrimSpace(valueAsString(metadata["project_id"])); v != "" {
		return v
	}
	return ""
}

func normalizeAuthID(id string) string {
	return filepath.ToSlash(filepath.Clean(id))
}

func normalizeLineEndings(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}
