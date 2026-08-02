package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"video-production-console/internal/assets"
	"video-production-console/internal/baokuan"
	"video-production-console/internal/codex"
	"video-production-console/internal/config"
	"video-production-console/internal/httpapi"
	"video-production-console/internal/obsidian"
	"video-production-console/internal/realtime"
	"video-production-console/internal/store"
	"video-production-console/internal/webui"
)

// Options provides dependencies and settings used by the application.
type Options struct {
	Config        config.Config
	DB            *sql.DB
	AssetService  *assets.Service
	Realtime      *realtime.Hub
	Scheduler     codex.Scheduler
	Obsidian      obsidian.Service
	BaokuanClient *baokuan.Client
	MCPExecutable string
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
		assetsHandler := httpapi.NewAssetsHandler(options.DB, assetService)
		mux.Handle("/api/assets/", assetsHandler)
		tasksHandler := httpapi.NewTasksHandler(options.DB, options.Scheduler)
		mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/tasks") && options.Scheduler != nil {
				tasksHandler.ServeHTTP(w, r)
				return
			}
			projects.ServeHTTP(w, r)
		})
		tasks := store.NewTaskRepository(options.DB)
		hub := options.Realtime
		if hub == nil {
			hub = realtime.NewHub(tasks)
		}
		mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
			const prefix = "/api/tasks/"
			path := r.URL.Path
			if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/events") {
				http.NotFound(w, r)
				return
			}
			taskID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/events")
			if taskID == "" || strings.Contains(taskID, "/") {
				http.NotFound(w, r)
				return
			}
			hub.Handler(w, r, taskID)
		})
		// Mount task commands separately; the event route above remains the
		// narrowly-scoped WebSocket endpoint.
		if options.Scheduler != nil {
			mux.Handle("/api/tasks", tasksHandler)
		}
		mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				var value string
				if err := options.DB.QueryRowContext(r.Context(), `SELECT value FROM settings WHERE key='max_codex_concurrency'`).Scan(&value); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"max_codex_concurrency":` + value + `}`))
				return
			}
			if r.Method != http.MethodPut {
				http.NotFound(w, r)
				return
			}
			var in struct {
				Max int `json:"max_codex_concurrency"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil || in.Max < 1 || in.Max > 4 {
				http.Error(w, "max_codex_concurrency must be 1-4", 400)
				return
			}
			if options.Scheduler != nil {
				if err := options.Scheduler.SetLimit(in.Max); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
			}
			if _, err := options.DB.ExecContext(r.Context(), `UPDATE settings SET value=? WHERE key='max_codex_concurrency'`, strconv.Itoa(in.Max)); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"max_codex_concurrency":` + strconv.Itoa(in.Max) + `}`))
		})
	}
	client := options.BaokuanClient
	if client == nil && options.Config.BaokuanBaseURL != "" {
		client = baokuan.NewClient(options.Config.BaokuanBaseURL)
	}
	if client != nil {
		deps := httpapi.NewDependenciesHandler(client, options.Config.CodexBinaryPath, options.MCPExecutable)
		mux.Handle("/api/dependencies", deps)
		mux.Handle("/api/dependencies/", deps)
		mux.Handle("/api/library/", deps)
	}
	mux.HandleFunc("GET /api/obsidian", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(options.Obsidian.Health())
	})
	mux.Handle("/", webui.Handler())
	return &App{handler: mux}
}

// Handler returns the application's HTTP handler.
func (a *App) Handler() http.Handler {
	return a.handler
}
