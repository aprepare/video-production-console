package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type completionRetryStub struct {
	taskID string
	err    error
}

func (s *completionRetryStub) RetryOutput(_ context.Context, taskID string) error {
	s.taskID = taskID
	return s.err
}

func TestCompletionRetryHandlerReprocessesRetainedOutput(t *testing.T) {
	stub := &completionRetryStub{}
	handler := NewCompletionRetryHandler(stub)
	request := httptest.NewRequest(http.MethodPost, "/api/tasks/task-1/retry-completion", nil)
	request.SetPathValue("id", "task-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || stub.taskID != "task-1" {
		t.Fatalf("status=%d task=%q body=%s", recorder.Code, stub.taskID, recorder.Body.String())
	}

	stub.err = errors.New("not eligible")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
