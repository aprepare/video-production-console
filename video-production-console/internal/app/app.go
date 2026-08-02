package app

import (
	"encoding/json"
	"net/http"

	"video-production-console/internal/config"
)

// Options provides dependencies and settings used by the application.
type Options struct {
	Config config.Config
}

// App is the HTTP application.
type App struct {
	handler http.Handler
}

// New constructs the application and its routes.
func New(_ Options) *App {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]string{"status": "ok"})
	})
	return &App{handler: mux}
}

// Handler returns the application's HTTP handler.
func (a *App) Handler() http.Handler {
	return a.handler
}
