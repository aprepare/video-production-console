package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type capturedRecord struct {
	Message   string `json:"msg"`
	RequestID string `json:"request_id"`
	TaskID    string `json:"task_id"`
}

func captureDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	restore := slog.Default()
	t.Cleanup(func() { slog.SetDefault(restore) })
	buffer := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(buffer, nil)))
	return buffer
}

func decodeRecords(t *testing.T, buffer *bytes.Buffer) []capturedRecord {
	t.Helper()
	var records []capturedRecord
	decoder := json.NewDecoder(bytes.NewReader(buffer.Bytes()))
	for decoder.More() {
		var record capturedRecord
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode log record: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func TestRequestIDCorrelatesHandlerLogsAndResponseHeader(t *testing.T) {
	buffer := captureDefaultLogger(t)

	handler := RequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		LoggerFrom(request.Context()).Error("first event")
		LoggerFrom(request.Context()).Error("second event")
		response.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))

	records := decodeRecords(t, buffer)
	if len(records) != 2 {
		t.Fatalf("expected 2 log records, got %d: %s", len(records), buffer.String())
	}
	if records[0].RequestID == "" {
		t.Fatal("first record carries no request_id")
	}
	if records[0].RequestID != records[1].RequestID {
		t.Fatalf("request_id differs within one request: %q vs %q", records[0].RequestID, records[1].RequestID)
	}
	header := recorder.Header().Get(RequestIDHeader)
	if header == "" {
		t.Fatal("response is missing the X-Request-Id header")
	}
	if header != records[0].RequestID {
		t.Fatalf("response header %q does not match logged request_id %q", header, records[0].RequestID)
	}
}

func TestRequestIDPropagatesUpstreamIdentifier(t *testing.T) {
	buffer := captureDefaultLogger(t)

	handler := RequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := RequestIDFrom(request.Context()); got != "upstream-42" {
			t.Errorf("RequestIDFrom = %q, want upstream-42", got)
		}
		LoggerFrom(request.Context()).Error("handler event")
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	request.Header.Set(RequestIDHeader, "upstream-42")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get(RequestIDHeader); got != "upstream-42" {
		t.Fatalf("response header = %q, want upstream-42", got)
	}
	records := decodeRecords(t, buffer)
	if len(records) != 1 || records[0].RequestID != "upstream-42" {
		t.Fatalf("unexpected records: %+v", records)
	}
}

func TestRequestIDRejectsUnsafeUpstreamIdentifier(t *testing.T) {
	for name, value := range map[string]string{
		"control characters": "bad\r\nid",
		"too long":           strings.Repeat("a", maxRequestIDLength+1),
		"blank":              "   ",
	} {
		handler := RequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {}))
		request := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
		request.Header.Set(RequestIDHeader, value)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		got := recorder.Header().Get(RequestIDHeader)
		if got == "" || got == strings.TrimSpace(value) {
			t.Fatalf("%s: expected a generated identifier, got %q", name, got)
		}
	}
}

func TestLoggerFromCombinesRequestAndTaskIdentifiers(t *testing.T) {
	buffer := captureDefaultLogger(t)

	ctx := WithTaskID(WithRequestID(context.Background(), "request-1"), "task-1")
	LoggerFrom(ctx).Error("correlated event")

	records := decodeRecords(t, buffer)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].RequestID != "request-1" || records[0].TaskID != "task-1" {
		t.Fatalf("unexpected record: %+v", records[0])
	}
	if got := TaskIDFrom(ctx); got != "task-1" {
		t.Fatalf("TaskIDFrom = %q, want task-1", got)
	}
}

func TestLoggerFromFallsBackToDefault(t *testing.T) {
	if LoggerFrom(context.Background()) != slog.Default() {
		t.Fatal("a context without a logger must fall back to the default logger")
	}
	//nolint:staticcheck // a nil context must not panic on background paths.
	if LoggerFrom(nil) != slog.Default() {
		t.Fatal("a nil context must fall back to the default logger")
	}
}
