package logging

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShouldAsyncFlushPayloadForLargeInlineImage(t *testing.T) {
	payload := []byte(`{"type":"input_image","image_url":"data:image/png;base64,` + strings.Repeat("A", inlineImageAsyncThresholdBytes) + `"}`)
	if !shouldAsyncFlushPayload(payload) {
		t.Fatalf("expected large inline base64 image payload to use async flush")
	}
}

func TestFileRequestLoggerLogRequestWritesFullPayloads(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewFileRequestLogger(true, tmpDir, "", 0, 15)

	requestBody := []byte(`{"model":"gpt-5-codex","input":"hello"}`)
	apiRequest := []byte("=== API REQUEST 1 ===\nUpstream URL: https://api.example.com/v1/responses\nBody:\n{\"model\":\"gpt-5-codex\"}\n")
	apiResponse := []byte("=== API RESPONSE 1 ===\nStatus: 200\nBody:\n{\"id\":\"resp_123\"}\n")
	responseBody := []byte(`{"id":"chatcmpl-123","object":"chat.completion"}`)

	err := logger.LogRequest(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{
			"Content-Type":  {"application/json"},
			"Authorization": {"Bearer secret-token"},
		},
		requestBody,
		http.StatusOK,
		map[string][]string{
			"Content-Type": {"application/json"},
		},
		responseBody,
		apiRequest,
		apiResponse,
		nil,
		"request-log-1",
		time.Now(),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("LogRequest returned error: %v", err)
	}

	content := readSingleLogFile(t, tmpDir)
	assertContains(t, content, "=== REQUEST INFO ===")
	assertContains(t, content, "=== REQUEST BODY ===")
	assertContains(t, content, string(requestBody))
	assertContains(t, content, "Authorization: Bearer ")
	assertContains(t, content, "Authorization: Bearer secr...oken")
	assertContains(t, content, "=== API REQUEST 1 ===")
	assertContains(t, content, "https://api.example.com/v1/responses")
	assertContains(t, content, "=== API RESPONSE 1 ===")
	assertContains(t, content, "{\"id\":\"resp_123\"}")
	assertContains(t, content, "=== RESPONSE ===")
	assertContains(t, content, string(responseBody))
}

func TestFileRequestLoggerLogRequestRedactsEncryptedContentAndOmitsSSEAPIBody(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewFileRequestLogger(true, tmpDir, "", 0, 15)

	requestBody := []byte(`{"model":"gpt-5-codex","input":"hello"}`)
	apiRequest := []byte("=== API REQUEST 1 ===\nUpstream URL: https://api.example.com/v1/responses\nBody:\n{\"include\":[\"reasoning.encrypted_content\"]}\n")
	apiResponse := []byte("=== API RESPONSE 1 ===\nStatus: 200\nBody:\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"reasoning\",\"encrypted_content\":\"secret-reasoning\"}]}}\n")
	responseBody := []byte(`{"output":[{"type":"reasoning","encrypted_content":"secret-final"}]}`)

	err := logger.LogRequest(
		"/v1/responses",
		http.MethodPost,
		map[string][]string{
			"Content-Type": {"application/json"},
		},
		requestBody,
		http.StatusOK,
		map[string][]string{
			"Content-Type": {"application/json"},
		},
		responseBody,
		apiRequest,
		apiResponse,
		nil,
		"request-log-redact-1",
		time.Now(),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("LogRequest returned error: %v", err)
	}

	content := readSingleLogFile(t, tmpDir)
	assertContains(t, content, `[REDACTED]`)
	assertContains(t, content, `[event-stream body omitted from log]`)
	assertNotContains(t, content, "secret-reasoning")
	assertNotContains(t, content, "secret-final")
	assertNotContains(t, content, "response.output_text.delta")
}

func TestFileRequestLoggerLogRequestEventuallyWritesLargeInlineImagePayload(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewFileRequestLogger(true, tmpDir, "", 0, 15)

	requestBody := []byte(`{"type":"input_image","image_url":"data:image/png;base64,` + strings.Repeat("A", inlineImageAsyncThresholdBytes) + `"}`)
	err := logger.LogRequest(
		"/v1/responses",
		http.MethodPost,
		map[string][]string{
			"Content-Type": {"application/json"},
		},
		requestBody,
		http.StatusOK,
		map[string][]string{
			"Content-Type": {"application/json"},
		},
		[]byte(`{"id":"resp_large"}`),
		nil,
		nil,
		nil,
		"request-log-async-1",
		time.Now(),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("LogRequest returned error: %v", err)
	}

	content := waitForSingleLogFile(t, tmpDir, 2*time.Second)
	assertContains(t, content, "data:image/png;base64,")
}

func TestFileRequestLoggerLogStreamingRequestWritesFullPayloads(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewFileRequestLogger(true, tmpDir, "", 0, 15)

	writer, err := logger.LogStreamingRequest(
		"/v1/responses",
		http.MethodPost,
		map[string][]string{
			"Content-Type": {"application/json"},
		},
		[]byte(`{"model":"gpt-5-codex","stream":true}`),
		"request-log-stream-1",
	)
	if err != nil {
		t.Fatalf("LogStreamingRequest returned error: %v", err)
	}

	if err := writer.WriteAPIRequest([]byte("=== API REQUEST 1 ===\nUpstream URL: https://chatgpt.com/backend-api/codex/responses\nBody:\n{\"stream\":true}\n")); err != nil {
		t.Fatalf("WriteAPIRequest returned error: %v", err)
	}
	if err := writer.WriteStatus(http.StatusOK, map[string][]string{
		"Content-Type": {"text/event-stream"},
	}); err != nil {
		t.Fatalf("WriteStatus returned error: %v", err)
	}
	writer.WriteChunkAsync([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"))
	if err := writer.WriteAPIResponse([]byte("=== API RESPONSE 1 ===\nStatus: 200\nBody:\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n")); err != nil {
		t.Fatalf("WriteAPIResponse returned error: %v", err)
	}
	writer.SetFirstChunkTimestamp(time.Now())
	if err := writer.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	content := readSingleLogFile(t, tmpDir)
	assertContains(t, content, "=== REQUEST INFO ===")
	assertContains(t, content, "=== API REQUEST 1 ===")
	assertContains(t, content, "https://chatgpt.com/backend-api/codex/responses")
	assertContains(t, content, "=== API RESPONSE 1 ===")
	assertContains(t, content, "[event-stream body omitted from log]")
	assertContains(t, content, "=== RESPONSE ===")
	assertContains(t, content, "text/event-stream")
	assertNotContains(t, content, "response.output_text.delta")
}

func TestCleanupExpiredRequestLogsRemovesOnlyOldRequestLogs(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewFileRequestLogger(true, tmpDir, "", 0, 15)

	oldRequestLog := filepath.Join(tmpDir, "v1-responses-old.log")
	if err := os.WriteFile(oldRequestLog, []byte("old"), 0o644); err != nil {
		t.Fatalf("failed to write old request log: %v", err)
	}
	oldTime := time.Now().Add(-16 * 24 * time.Hour)
	if err := os.Chtimes(oldRequestLog, oldTime, oldTime); err != nil {
		t.Fatalf("failed to age old request log: %v", err)
	}

	protectedMain := filepath.Join(tmpDir, "main.log")
	if err := os.WriteFile(protectedMain, []byte("main"), 0o644); err != nil {
		t.Fatalf("failed to write main log: %v", err)
	}
	if err := os.Chtimes(protectedMain, oldTime, oldTime); err != nil {
		t.Fatalf("failed to age main log: %v", err)
	}

	recentRequestLog := filepath.Join(tmpDir, "v1-responses-new.log")
	if err := os.WriteFile(recentRequestLog, []byte("new"), 0o644); err != nil {
		t.Fatalf("failed to write recent request log: %v", err)
	}

	removed, err := logger.cleanupExpiredRequestLogs(15)
	if err != nil {
		t.Fatalf("cleanupExpiredRequestLogs returned error: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected exactly one removed request log, got %d", removed)
	}
	if _, err := os.Stat(oldRequestLog); !os.IsNotExist(err) {
		t.Fatalf("expected old request log to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(protectedMain); err != nil {
		t.Fatalf("expected main.log to be preserved, stat err=%v", err)
	}
	if _, err := os.Stat(recentRequestLog); err != nil {
		t.Fatalf("expected recent request log to remain, stat err=%v", err)
	}
}

func readSingleLogFile(t *testing.T, dir string) string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}

	var logFiles []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		logFiles = append(logFiles, filepath.Join(dir, entry.Name()))
	}
	if len(logFiles) != 1 {
		t.Fatalf("expected exactly one log file, got %d: %v", len(logFiles), logFiles)
	}

	data, err := os.ReadFile(logFiles[0])
	if err != nil {
		t.Fatalf("failed to read log file %s: %v", logFiles[0], err)
	}
	return string(data)
}

func waitForSingleLogFile(t *testing.T, dir string, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
					continue
				}
				data, errRead := os.ReadFile(filepath.Join(dir, entry.Name()))
				if errRead == nil {
					return string(data)
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for request log file in %s", dir)
	return ""
}

func assertContains(t *testing.T, content string, want string) {
	t.Helper()
	if !strings.Contains(content, want) {
		t.Fatalf("log content missing %q\nfull content:\n%s", want, content)
	}
}

func assertNotContains(t *testing.T, content string, want string) {
	t.Helper()
	if strings.Contains(content, want) {
		t.Fatalf("log content unexpectedly contains %q\nfull content:\n%s", want, content)
	}
}
