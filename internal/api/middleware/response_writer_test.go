package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestExtractRequestBodyPrefersOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	wrapper := &ResponseWriterWrapper{
		requestInfo: &RequestInfo{Body: []byte("original-body")},
	}

	body := wrapper.extractRequestBody(c)
	if string(body) != "original-body" {
		t.Fatalf("request body = %q, want %q", string(body), "original-body")
	}

	c.Set(requestBodyOverrideContextKey, []byte("override-body"))
	body = wrapper.extractRequestBody(c)
	if string(body) != "override-body" {
		t.Fatalf("request body = %q, want %q", string(body), "override-body")
	}
}

func TestExtractRequestBodySupportsStringOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	wrapper := &ResponseWriterWrapper{}
	c.Set(requestBodyOverrideContextKey, "override-as-string")

	body := wrapper.extractRequestBody(c)
	if string(body) != "override-as-string" {
		t.Fatalf("request body = %q, want %q", string(body), "override-as-string")
	}
}

func TestWithAPITimingAppendsStreamingTimingSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)

	requestStartedAt := time.Date(2026, 4, 11, 19, 9, 6, 312346000, time.FixedZone("UTC+8", 8*3600))
	upstreamResponseAt := requestStartedAt.Add(633 * time.Millisecond)
	upstreamFirstChunkAt := requestStartedAt.Add(400 * time.Second)
	downstreamFirstChunkAt := upstreamFirstChunkAt.Add(15 * time.Millisecond)

	ginCtx.Set(apiUpstreamResponseTimestampContextKey, upstreamResponseAt)
	ginCtx.Set(apiUpstreamFirstChunkTimestampContextKey, upstreamFirstChunkAt)
	ginCtx.Set(apiDownstreamFirstChunkTimestampContextKey, downstreamFirstChunkAt)

	wrapper := NewResponseWriterWrapper(ginCtx.Writer, nil, &RequestInfo{Timestamp: requestStartedAt}, ginCtx)
	payload := []byte("=== API RESPONSE 1 ===\nTimestamp: 2026-04-11T19:09:06.945251389+08:00\n\nStatus: 200\nBody:\n[event-stream body omitted from log]\n")

	augmented := string(wrapper.withAPITiming(payload, ginCtx))

	for _, want := range []string{
		"=== API TIMING ===",
		"Request Started At: 2026-04-11T19:09:06.312346+08:00",
		"Upstream HTTP Response At: 2026-04-11T19:09:06.945346+08:00",
		"Upstream First Chunk At: 2026-04-11T19:15:46.312346+08:00",
		"Downstream First Forwarded Chunk At: 2026-04-11T19:15:46.327346+08:00",
		"Request -> Upstream HTTP Response: 633ms",
		"Request -> Upstream First Chunk: 6m40s",
		"Request -> Downstream First Forwarded Chunk: 6m40.015s",
		"Upstream HTTP Response -> Upstream First Chunk: 6m39.367s",
		"Upstream HTTP Response -> Downstream First Forwarded Chunk: 6m39.382s",
	} {
		if !strings.Contains(augmented, want) {
			t.Fatalf("augmented API response missing %q\nfull content:\n%s", want, augmented)
		}
	}
}

func TestWithAPITimingDoesNotDuplicateSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)

	wrapper := NewResponseWriterWrapper(ginCtx.Writer, nil, &RequestInfo{Timestamp: time.Now()}, ginCtx)
	payload := []byte("=== API RESPONSE 1 ===\n=== API TIMING ===\n")

	augmented := string(wrapper.withAPITiming(payload, ginCtx))
	if strings.Count(augmented, "=== API TIMING ===") != 1 {
		t.Fatalf("expected a single timing summary, got:\n%s", augmented)
	}
}
