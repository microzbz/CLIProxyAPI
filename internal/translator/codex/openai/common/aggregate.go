package common

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

var dataTag = []byte("data:")

type AggregatedCodexSSE struct {
	ResponseID           string
	Model                string
	CreatedAt            int64
	CompletedResponseRaw []byte
	OutputItems          [][]byte
	MessageText          string
	ReasoningText        string
}

func CollectCodexSSEDataLines(rawJSON []byte) [][]byte {
	lines := bytes.Split(rawJSON, []byte("\n"))
	chunks := make([][]byte, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}
		payload := bytes.TrimSpace(line[len(dataTag):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		chunks = append(chunks, payload)
	}
	return chunks
}

func AggregateCodexSSE(rawJSON []byte) AggregatedCodexSSE {
	chunks := CollectCodexSSEDataLines(rawJSON)
	agg := AggregatedCodexSSE{
		OutputItems: make([][]byte, 0),
	}
	if len(chunks) == 0 {
		return agg
	}

	var messageFromItems strings.Builder
	var messageFromDeltas strings.Builder
	var reasoningFromItems strings.Builder
	var reasoningFromDeltas strings.Builder

	for _, chunk := range chunks {
		root := gjson.ParseBytes(chunk)
		switch root.Get("type").String() {
		case "response.created":
			resp := root.Get("response")
			if resp.Exists() {
				if agg.ResponseID == "" {
					agg.ResponseID = resp.Get("id").String()
				}
				if agg.Model == "" {
					agg.Model = resp.Get("model").String()
				}
				if agg.CreatedAt == 0 {
					agg.CreatedAt = resp.Get("created_at").Int()
				}
			}
		case "response.output_text.delta":
			if delta := root.Get("delta"); delta.Exists() {
				messageFromDeltas.WriteString(delta.String())
			}
		case "response.output_text.done":
			if messageFromDeltas.Len() == 0 {
				if text := root.Get("text"); text.Exists() {
					messageFromDeltas.WriteString(text.String())
				}
			}
		case "response.reasoning_summary_text.delta":
			if delta := root.Get("delta"); delta.Exists() {
				reasoningFromDeltas.WriteString(delta.String())
			}
		case "response.output_item.done":
			item := root.Get("item")
			if !item.Exists() {
				continue
			}
			agg.OutputItems = append(agg.OutputItems, []byte(item.Raw))
			switch item.Get("type").String() {
			case "message":
				messageFromItems.WriteString(extractCodexMessageText(item))
			case "reasoning":
				reasoningFromItems.WriteString(extractCodexReasoningText(item))
			}
		case "response.completed":
			resp := root.Get("response")
			if !resp.Exists() {
				continue
			}
			agg.CompletedResponseRaw = []byte(resp.Raw)
			if id := resp.Get("id").String(); id != "" {
				agg.ResponseID = id
			}
			if model := resp.Get("model").String(); model != "" {
				agg.Model = model
			}
			if createdAt := resp.Get("created_at").Int(); createdAt > 0 {
				agg.CreatedAt = createdAt
			}
		}
	}

	if messageFromItems.Len() > 0 {
		agg.MessageText = messageFromItems.String()
	} else {
		agg.MessageText = messageFromDeltas.String()
	}
	if reasoningFromItems.Len() > 0 {
		agg.ReasoningText = reasoningFromItems.String()
	} else {
		agg.ReasoningText = reasoningFromDeltas.String()
	}

	return agg
}

func extractCodexMessageText(item gjson.Result) string {
	var builder strings.Builder
	content := item.Get("content")
	if !content.IsArray() {
		return ""
	}
	for _, part := range content.Array() {
		if part.Get("type").String() != "output_text" {
			continue
		}
		if text := part.Get("text").String(); text != "" {
			builder.WriteString(text)
		}
	}
	return builder.String()
}

func extractCodexReasoningText(item gjson.Result) string {
	var builder strings.Builder
	if summary := item.Get("summary"); summary.IsArray() {
		for _, part := range summary.Array() {
			if part.Get("type").String() != "summary_text" {
				continue
			}
			if text := part.Get("text").String(); text != "" {
				builder.WriteString(text)
			}
		}
	}
	if builder.Len() > 0 {
		return builder.String()
	}
	content := item.Get("content")
	if content.IsArray() {
		for _, part := range content.Array() {
			if text := part.Get("text").String(); text != "" {
				builder.WriteString(text)
			}
		}
	}
	return builder.String()
}
