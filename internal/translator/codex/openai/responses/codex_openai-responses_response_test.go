package responses

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertCodexResponseToOpenAIResponsesNonStream_AggregatesOutputItemsWhenCompletedOutputEmpty(t *testing.T) {
	raw := []byte(`event: response.created
data: {"type":"response.created","response":{"id":"resp_123","created_at":1776221988,"model":"gpt-5.4","status":"in_progress","output":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"id":"msg_123","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"text":"你好啊"}],"role":"assistant"},"output_index":0,"sequence_number":24}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_123","object":"response","created_at":1776221988,"status":"completed","model":"gpt-5.4","output":[],"usage":{"input_tokens":8,"input_tokens_details":{"cached_tokens":0},"output_tokens":22,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":30}},"sequence_number":25}
`)

	out := ConvertCodexResponseToOpenAIResponsesNonStream(context.Background(), "", nil, nil, raw, nil)

	if got := gjson.GetBytes(out, "output.0.type").String(); got != "message" {
		t.Fatalf("output.0.type = %q, want %q: %s", got, "message", string(out))
	}
	if got := gjson.GetBytes(out, "output.0.content.0.text").String(); got != "你好啊" {
		t.Fatalf("output.0.content.0.text = %q, want %q: %s", got, "你好啊", string(out))
	}
	if got := gjson.GetBytes(out, "usage.output_tokens").Int(); got != 22 {
		t.Fatalf("usage.output_tokens = %d, want %d: %s", got, 22, string(out))
	}
}
