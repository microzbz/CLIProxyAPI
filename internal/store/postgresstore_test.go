package store

import (
	"context"
	"testing"
)

func TestPostgresStorePersistConfigNoop(t *testing.T) {
	store := &PostgresStore{}
	if err := store.PersistConfig(context.Background()); err != nil {
		t.Fatalf("PersistConfig() error = %v, want nil", err)
	}
}
