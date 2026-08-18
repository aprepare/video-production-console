package httpapi

import (
	"context"
	"errors"
	"net/http"

	"video-production-console/internal/domain"
	"video-production-console/internal/montage"
	"video-production-console/internal/store"
)

type montageRetryer interface {
	Retry(context.Context, string) (domain.RegistrationAttempt, error)
}

type montageHandler struct{ retryer montageRetryer }

func NewMontageHandler(retryer montageRetryer) http.Handler {
	h := &montageHandler{retryer: retryer}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/tasks/{id}/retry-registration", h.retry)
	return mux
}

func (h *montageHandler) retry(w http.ResponseWriter, r *http.Request) {
	if h.retryer == nil {
		writeError(w, http.StatusServiceUnavailable, "registration_unavailable", "Montage registration is unavailable.")
		return
	}
	attempt, err := h.retryer.Retry(r.Context(), r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRegistrationNotFound):
			writeError(w, http.StatusNotFound, "registration_not_found", "No retained montage workspace was found.")
		case errors.Is(err, store.ErrRegistrationActive):
			writeError(w, http.StatusConflict, "registration_active", "Montage registration is already running.")
		case errors.Is(err, montage.ErrCoordinatorStopped):
			writeError(w, http.StatusServiceUnavailable, "registration_unavailable", "Montage registration is unavailable.")
		case errors.Is(err, montage.ErrRegistrationBackpressure):
			writeError(w, http.StatusTooManyRequests, "registration_backpressure", "Montage registration queue is full.")
		case errors.Is(err, store.ErrRegistrationInputInvalid), errors.Is(err, store.ErrRegistrationNotRetryable):
			writeError(w, http.StatusUnprocessableEntity, "registration_retry_invalid", "The retained montage registration cannot be retried.")
		default:
			writeError(w, http.StatusInternalServerError, "registration_retry_failed", "Montage registration could not be queued.")
		}
		return
	}
	writeJSON(w, http.StatusAccepted, attempt)
}
