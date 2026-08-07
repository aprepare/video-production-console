package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/montage"
	"video-production-console/internal/store"
)

type montageRetryStub struct{ err error }

func (s montageRetryStub) Retry(context.Context, string) (domain.RegistrationAttempt, error) {
	return domain.RegistrationAttempt{}, s.err
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
