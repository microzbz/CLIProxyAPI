package registry

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type stubCatalogPersistence struct {
	loadPayload   []byte
	loadSource    string
	loadUpdatedAt time.Time
	loadFound     bool
	loadErr       error

	savedPayload []byte
	savedSource  string
	saveErr      error
}

func (s *stubCatalogPersistence) LoadModelsCatalog(context.Context) ([]byte, string, time.Time, bool, error) {
	return append([]byte(nil), s.loadPayload...), s.loadSource, s.loadUpdatedAt, s.loadFound, s.loadErr
}

func (s *stubCatalogPersistence) SaveModelsCatalog(_ context.Context, payload []byte, source string) error {
	s.savedPayload = append([]byte(nil), payload...)
	s.savedSource = source
	return s.saveErr
}

func resetCatalogGlobalsForTest(t *testing.T) {
	t.Helper()
	if err := loadModelsFromBytes(embeddedModelsJSON, "test-reset"); err != nil {
		t.Fatalf("loadModelsFromBytes(reset) error = %v", err)
	}
	catalogPersistenceMu.Lock()
	catalogPersistence = nil
	catalogPersistenceMu.Unlock()
	refreshCallbackMu.Lock()
	refreshCallback = nil
	pendingRefreshChanges = nil
	refreshCallbackMu.Unlock()
}

func containsModel(models []*ModelInfo, id string) bool {
	for _, model := range models {
		if model != nil && model.ID == id {
			return true
		}
	}
	return false
}

func TestSetCatalogPersistenceLoadsPersistedCatalog(t *testing.T) {
	resetCatalogGlobalsForTest(t)
	defer resetCatalogGlobalsForTest(t)

	if containsModel(GetCodexFreeModels(), "unit-test-codex-free-model") {
		t.Fatal("unexpected test model present before persistence load")
	}

	var persisted staticModelsJSON
	if err := json.Unmarshal(embeddedModelsJSON, &persisted); err != nil {
		t.Fatalf("json.Unmarshal(embeddedModelsJSON) error = %v", err)
	}
	persisted.CodexFree = append(persisted.CodexFree, &ModelInfo{
		ID:          "unit-test-codex-free-model",
		Object:      "model",
		Created:     1776384000,
		OwnedBy:     "openai",
		Type:        "openai",
		DisplayName: "Unit Test Codex Free Model",
	})
	payload, err := json.Marshal(&persisted)
	if err != nil {
		t.Fatalf("json.Marshal(persisted) error = %v", err)
	}

	stub := &stubCatalogPersistence{
		loadPayload:   payload,
		loadSource:    "postgres://catalog",
		loadUpdatedAt: time.Date(2026, 4, 17, 1, 2, 3, 0, time.UTC),
		loadFound:     true,
	}
	if err := SetCatalogPersistence(context.Background(), stub); err != nil {
		t.Fatalf("SetCatalogPersistence() error = %v", err)
	}

	if !containsModel(GetCodexFreeModels(), "unit-test-codex-free-model") {
		t.Fatal("expected persisted catalog model to be loaded into registry")
	}
}

func TestPersistModelsCatalogUsesConfiguredBackend(t *testing.T) {
	resetCatalogGlobalsForTest(t)
	defer resetCatalogGlobalsForTest(t)

	stub := &stubCatalogPersistence{}
	catalogPersistenceMu.Lock()
	catalogPersistence = stub
	catalogPersistenceMu.Unlock()

	payload := []byte(`{"codex-free":[{"id":"stub"}]}`)
	if err := persistModelsCatalog(context.Background(), payload, "https://example.test/models.json"); err != nil {
		t.Fatalf("persistModelsCatalog() error = %v", err)
	}
	if string(stub.savedPayload) != string(payload) {
		t.Fatalf("saved payload = %s, want %s", string(stub.savedPayload), string(payload))
	}
	if stub.savedSource != "https://example.test/models.json" {
		t.Fatalf("saved source = %q, want %q", stub.savedSource, "https://example.test/models.json")
	}
}

func TestPatchEmptyCatalogSectionsPreservesFallback(t *testing.T) {
	target := &staticModelsJSON{
		CodexFree: []*ModelInfo{{ID: "target-codex"}},
		Qwen:      nil,
	}
	fallback := &staticModelsJSON{
		Qwen: []*ModelInfo{{ID: "fallback-qwen"}},
	}

	patchEmptyCatalogSections(target, fallback, "https://example.test/models.json")

	if len(target.Qwen) != 1 || target.Qwen[0] == nil || target.Qwen[0].ID != "fallback-qwen" {
		t.Fatalf("target.Qwen = %#v, want fallback section to be preserved", target.Qwen)
	}
	if len(target.CodexFree) != 1 || target.CodexFree[0] == nil || target.CodexFree[0].ID != "target-codex" {
		t.Fatalf("target.CodexFree = %#v, want existing non-empty section unchanged", target.CodexFree)
	}
}
