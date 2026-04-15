package thinking_test

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/thinking/provider/codex"
	"github.com/tidwall/gjson"
)

func TestApplyThinking_CodexNormalizesMinimalReasoningEffortToLow(t *testing.T) {
	reg := registry.GetGlobalRegistry()
	clientID := "test-codex-minimal-" + t.Name()
	modelID := "test-codex-level-model"
	reg.RegisterClient(clientID, "codex", []*registry.ModelInfo{{
		ID: modelID,
		Thinking: &registry.ThinkingSupport{
			Levels: []string{"low", "medium", "high", "xhigh"},
		},
	}})
	t.Cleanup(func() {
		reg.UnregisterClient(clientID)
	})

	out, err := thinking.ApplyThinking(
		[]byte(`{"model":"test-codex-level-model","reasoning":{"effort":"minimal"}}`),
		modelID,
		"codex",
		"codex",
		"codex",
	)
	if err != nil {
		t.Fatalf("ApplyThinking() error = %v", err)
	}

	if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "low" {
		t.Fatalf("reasoning.effort = %q, want %q: %s", got, "low", string(out))
	}
}
