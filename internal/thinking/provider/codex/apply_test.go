package codex

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	"github.com/tidwall/gjson"
)

func TestApply_NormalizesMinimalReasoningEffortToLow(t *testing.T) {
	applier := NewApplier()
	modelInfo := &registry.ModelInfo{
		ID: "gpt-5.4",
		Thinking: &registry.ThinkingSupport{
			Levels: []string{"low", "medium", "high", "xhigh"},
		},
	}

	out, err := applier.Apply([]byte(`{"model":"gpt-5.4"}`), thinking.ThinkingConfig{
		Mode:  thinking.ModeLevel,
		Level: thinking.LevelMinimal,
	}, modelInfo)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "low" {
		t.Fatalf("reasoning.effort = %q, want %q: %s", got, "low", string(out))
	}
}
