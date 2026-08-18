package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/montage"
	"video-production-console/internal/store"
)

type montageRetryStub struct {
	err     error
	attempt domain.RegistrationAttempt
}

func (s montageRetryStub) Retry(context.Context, string) (domain.RegistrationAttempt, error) {
	return s.attempt, s.err
}

func TestMontageRetryReturnsSnakeCaseAttempt(t *testing.T) {
	started := time.Date(2026, 8, 12, 7, 0, 0, 0, time.UTC)
	registered := "workspace/draft"
	attempt := domain.RegistrationAttempt{ID: "attempt-1", TaskID: "task", ManifestPath: "manifests/task.json", WorkspacePath: "workspace", State: domain.RegistrationQueued, Attempt: 2, RegisteredPath: &registered, StartedAt: started}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tasks/task/retry-registration", nil)
	NewMontageHandler(montageRetryStub{attempt: attempt}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertSnakeCaseKeys(t, "/api/tasks/task/retry-registration", recorder.Body.Bytes())
	var body struct {
		ID             string  `json:"id"`
		TaskID         string  `json:"task_id"`
		ManifestPath   string  `json:"manifest_path"`
		WorkspacePath  string  `json:"workspace_path"`
		State          string  `json:"state"`
		Attempt        int     `json:"attempt"`
		RegisteredPath *string `json:"registered_path"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != attempt.ID || body.TaskID != attempt.TaskID || body.ManifestPath != attempt.ManifestPath || body.WorkspacePath != attempt.WorkspacePath || body.State != string(domain.RegistrationQueued) || body.Attempt != 2 || body.RegisteredPath == nil || *body.RegisteredPath != registered {
		t.Fatalf("body=%+v raw=%s", body, recorder.Body.String())
	}
}

func TestMontageRetryMapsTypedErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not found", store.ErrRegistrationNotFound, http.StatusNotFound, "registration_not_found"},
		{"active", store.ErrRegistrationActive, http.StatusConflict, "registration_active"},
		{"stopped", montage.ErrCoordinatorStopped, http.StatusServiceUnavailable, "registration_unavailable"},
		{"backpressure", montage.ErrRegistrationBackpressure, http.StatusTooManyRequests, "registration_backpressure"},
		{"invalid retained input", store.ErrRegistrationInputInvalid, http.StatusUnprocessableEntity, "registration_retry_invalid"},
		{"wrapped invalid state", errors.Join(errors.New("retry"), store.ErrRegistrationNotRetryable), http.StatusUnprocessableEntity, "registration_retry_invalid"},
		{"internal", errors.New("database unavailable"), http.StatusInternalServerError, "registration_retry_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/tasks/task/retry-registration", nil)
			NewMontageHandler(montageRetryStub{err: test.err}).ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.status, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("body=%s does not contain stable code %q", recorder.Body.String(), test.code)
			}
		})
	}
}
