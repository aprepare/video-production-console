package logging

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// RequestIDHeader carries the correlation identifier of one HTTP request.
const RequestIDHeader = "X-Request-Id"

// maxRequestIDLength bounds an upstream-supplied identifier so it cannot turn
// log records into unbounded attacker-controlled payloads.
const maxRequestIDLength = 128

type contextKey int

const (
	loggerContextKey contextKey = iota
	requestIDContextKey
	taskIDContextKey
)

// LoggerFrom returns the logger annotated with the correlation identifiers
// carried by ctx, falling back to the process-wide logger.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if logger, ok := ctx.Value(loggerContextKey).(*slog.Logger); ok && logger != nil {
			return logger
		}
	}
	return slog.Default()
}

// WithRequestID annotates ctx and its logger with a request correlation
// identifier. An empty identifier leaves ctx unchanged.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	if ctx == nil || requestID == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, requestIDContextKey, requestID)
	return context.WithValue(ctx, loggerContextKey, LoggerFrom(ctx).With("request_id", requestID))
}

// RequestIDFrom returns the request correlation identifier carried by ctx.
func RequestIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

// WithTaskID annotates ctx and its logger with a task correlation identifier.
// An empty identifier leaves ctx unchanged.
func WithTaskID(ctx context.Context, taskID string) context.Context {
	taskID = strings.TrimSpace(taskID)
	if ctx == nil || taskID == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, taskIDContextKey, taskID)
	return context.WithValue(ctx, loggerContextKey, LoggerFrom(ctx).With("task_id", taskID))
}

// TaskIDFrom returns the task correlation identifier carried by ctx.
func TaskIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	taskID, _ := ctx.Value(taskIDContextKey).(string)
	return taskID
}

// TaskLogger returns the process-wide logger annotated with a task identifier.
// Use it on background paths that own a task but no request context.
func TaskLogger(taskID string) *slog.Logger {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return slog.Default()
	}
	return slog.Default().With("task_id", taskID)
}

// RequestID generates or propagates a request correlation identifier, exposes
// it to handler logging through the request context, and echoes it back to the
// caller. The response writer is passed through untouched so streaming and
// hijacking handlers keep working.
func RequestID(next http.Handler) http.Handler {
	if next == nil {
		return nil
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID := sanitizeRequestID(request.Header.Get(RequestIDHeader))
		if requestID == "" {
			requestID = uuid.NewString()
		}
		response.Header().Set(RequestIDHeader, requestID)
		next.ServeHTTP(response, request.WithContext(WithRequestID(request.Context(), requestID)))
	})
}

// sanitizeRequestID accepts a bounded printable ASCII identifier and rejects
// everything else, including header-splitting and control characters.
func sanitizeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxRequestIDLength {
		return ""
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return ""
		}
	}
	return value
}
