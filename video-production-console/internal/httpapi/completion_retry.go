package httpapi

import (
	"context"
	"net/http"
)

type CompletionRetryer interface {
	RetryOutput(context.Context, string) error
}

func NewCompletionRetryHandler(retryer CompletionRetryer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/tasks/{id}/retry-completion", func(w http.ResponseWriter, r *http.Request) {
		if retryer == nil {
			writeError(w, http.StatusServiceUnavailable, "completion_retry_unavailable", "Result retry is unavailable.")
			return
		}
		if err := retryer.RetryOutput(r.Context(), r.PathValue("id")); err != nil {
			writeError(w, http.StatusConflict, "completion_retry_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "reprocessing"})
	})
	return mux
}
