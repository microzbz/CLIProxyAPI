package responses

import (
	"bytes"
	"context"
	"strings"

	codexcommon "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/codex/openai/common"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ConvertCodexResponseToOpenAIResponses converts OpenAI Chat Completions streaming chunks
// to OpenAI Responses SSE events (response.*).

func ConvertCodexResponseToOpenAIResponses(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) [][]byte {
	if bytes.HasPrefix(rawJSON, []byte("data:")) {
		rawJSON = bytes.TrimSpace(rawJSON[5:])
		out := make([]byte, 0, len(rawJSON)+len("data: "))
		out = append(out, []byte("data: ")...)
		out = append(out, rawJSON...)
		return [][]byte{out}
	}
	return [][]byte{rawJSON}
}

// ConvertCodexResponseToOpenAIResponsesNonStream builds a single Responses JSON
// from a non-streaming OpenAI Chat Completions response.
func ConvertCodexResponseToOpenAIResponsesNonStream(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) []byte {
	if len(codexcommon.CollectCodexSSEDataLines(rawJSON)) > 0 {
		agg := codexcommon.AggregateCodexSSE(rawJSON)
		if len(agg.CompletedResponseRaw) == 0 {
			return []byte{}
		}

		resp := append([]byte(nil), agg.CompletedResponseRaw...)
		outputItems := make([][]byte, 0, len(agg.OutputItems)+2)
		hasMessageItem := false
		hasReasoningItem := false
		for _, itemRaw := range agg.OutputItems {
			item := gjson.ParseBytes(itemRaw)
			switch item.Get("type").String() {
			case "message":
				hasMessageItem = true
			case "reasoning":
				hasReasoningItem = true
			}
			outputItems = append(outputItems, itemRaw)
		}

		if !hasReasoningItem && strings.TrimSpace(agg.ReasoningText) != "" {
			reasoningItem := []byte(`{"type":"reasoning","encrypted_content":"","summary":[{"type":"summary_text","text":""}]}`)
			reasoningItem, _ = sjson.SetBytes(reasoningItem, "summary.0.text", agg.ReasoningText)
			outputItems = append([][]byte{reasoningItem}, outputItems...)
		}
		if !hasMessageItem && strings.TrimSpace(agg.MessageText) != "" {
			messageItem := []byte(`{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","annotations":[],"text":""}]}`)
			messageItem, _ = sjson.SetBytes(messageItem, "content.0.text", agg.MessageText)
			outputItems = append(outputItems, messageItem)
		}

		if len(outputItems) > 0 {
			resp, _ = sjson.SetRawBytes(resp, "output", []byte(`[]`))
			for _, itemRaw := range outputItems {
				resp, _ = sjson.SetRawBytes(resp, "output.-1", itemRaw)
			}
		}
		return resp
	}

	rootResult := gjson.ParseBytes(rawJSON)
	// Verify this is a response.completed event
	if rootResult.Get("type").String() != "response.completed" {
		return []byte{}
	}
	responseResult := rootResult.Get("response")
	return []byte(responseResult.Raw)
}
