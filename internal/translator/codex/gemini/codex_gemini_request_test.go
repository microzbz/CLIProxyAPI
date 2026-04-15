package gemini

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertGeminiRequestToCodex_NormalizesMinimalReasoningEffort(t *testing.T) {
	input := []byte(`{
		"generationConfig": {
			"thinkingConfig": {
				"thinkingLevel": "minimal"
			}
		},
		"contents": [
			{"role": "user", "parts": [{"text": "hello"}]}
		]
	}`)

	out := ConvertGeminiRequestToCodex("gpt-5.4", input, false)
	if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "low" {
		t.Fatalf("reasoning.effort = %q, want %q: %s", got, "low", string(out))
	}
}
