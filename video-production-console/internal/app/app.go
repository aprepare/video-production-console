package app

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"video-production-console/internal/assets"
	"video-production-console/internal/config"
	"video-production-console/internal/httpapi"
)

// Options provides dependencies and settings used by the application.
type Options struct {
	Config       config.Config
	DB           *sql.DB
	AssetService *assets.Service
}

// App is the HTTP application.
type App struct {
	handler http.Handler
}

// New constructs the application and its routes.
func New(options Options) *App {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]string{"status": "ok"})
	})
	if options.DB != nil {
		assetService := options.AssetService
		if assetService == nil {
			assetService = assets.NewService(options.Config.DataRoot)
		}
		accounts := httpapi.NewAccountsHandler(options.DB, assetService)
		mux.Handle("/api/accounts", accounts)
		mux.Handle("/api/accounts/", accounts)
		projects := httpapi.NewProjectsHandler(options.DB, assetService)
		mux.Handle("/api/projects", projects)
		mux.Handle("/api/projects/", projects)
	}
	return &App{handler: mux}
}

// Handler returns the application's HTTP handler.
func (a *App) Handler() http.Handler {
	return a.handler
}
