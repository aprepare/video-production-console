package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"video-production-console/internal/assets"
	consoleauth "video-production-console/internal/auth"
	"video-production-console/internal/baokuan"
	"video-production-console/internal/codex"
	"video-production-console/internal/codexapp"
	"video-production-console/internal/config"
	"video-production-console/internal/conversation"
	"video-production-console/internal/domain"
	"video-production-console/internal/history"
	"video-production-console/internal/httpapi"
	"video-production-console/internal/obsidian"
	"video-production-console/internal/realtime"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/skillregistry"
	"video-production-console/internal/store"
	"video-production-console/internal/webui"
)

// Options provides dependencies and settings used by the application.
type Options struct {
	Config          config.Config
	DB              *sql.DB
	AssetService    *assets.Service
	Realtime        *realtime.Hub
	Scheduler       codex.Scheduler
	Obsidian        obsidian.Service
	BaokuanClient   *baokuan.Client
	MCPExecutable   string
	AuthService     *consoleauth.Service
	Settings        *consoleSettings.Service
	Skills          *skillregistry.Service
	TaskPreparer    httpapi.TaskManifestPreparer
	Conversations   *conversation.Service
	AppServerHealth func() codexapp.Health
	History         *history.Service
	MontageRetryer  interface {
		Retry(context.Context, string) (domain.RegistrationAttempt, error)
	}
	CompletionRetryer httpapi.CompletionRetryer
	DesktopOpener     assets.DesktopOpener
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
		assetOptions := httpapi.AssetHandlerOptions{DesktopOpener: options.DesktopOpener}
		if options.Settings != nil {
			assetOptions.Runtime = options.Settings
		}
		assetsHandler := httpapi.NewAssetsHandler(options.DB, assetService, assetOptions)
		mux.Handle("/api/assets/", assetsHandler)
		var models httpapi.TaskModelResolver
		if options.Settings != nil {
			models = options.Settings
		}
		tasksHandler := httpapi.NewTasksHandler(options.DB, options.Scheduler, options.TaskPreparer, models)
		taskResultsHandler := httpapi.NewTaskResultsHandler(store.NewTaskRepository(options.DB))
		montageHandler := httpapi.NewMontageHandler(options.MontageRetryer)
		completionRetryHandler := httpapi.NewCompletionRetryHandler(options.CompletionRetryer)
		mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
			if options.Scheduler != nil && (strings.HasSuffix(r.URL.Path, "/tasks") || strings.HasSuffix(r.URL.Path, "/topic-card")) {
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
			if !strings.HasPrefix(path, prefix) {
				http.NotFound(w, r)
				return
			}
			if strings.HasSuffix(path, "/artifacts") || strings.HasSuffix(path, "/result") || strings.HasSuffix(path, "/diagnostics") || strings.HasSuffix(path, "/semantic-events") {
				taskResultsHandler.ServeHTTP(w, r)
				return
			}
			if strings.HasSuffix(path, "/retry-registration") {
				montageHandler.ServeHTTP(w, r)
				return
			}
			if strings.HasSuffix(path, "/retry-completion") {
				completionRetryHandler.ServeHTTP(w, r)
				return
			}
			if !strings.HasSuffix(path, "/events") {
				tasksHandler.ServeHTTP(w, r)
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
		if options.Settings != nil {
			mux.Handle("/api/settings", httpapi.NewSettingsHandler(settingsWithScheduler{service: options.Settings, scheduler: options.Scheduler}))
			mux.Handle("/api/settings/", httpapi.NewSettingsHandler(settingsWithScheduler{service: options.Settings, scheduler: options.Scheduler}))
		}
		if options.Skills != nil {
			skillsHandler := httpapi.NewSkillsHandler(options.Skills)
			mux.Handle("/api/skills", skillsHandler)
			mux.Handle("/api/skills/", skillsHandler)
		}
		ideasHandler := httpapi.NewIdeasHandler(options.DB, options.Scheduler, options.TaskPreparer, models)
		mux.Handle("/api/ideas", ideasHandler)
		mux.Handle("/api/ideas/", ideasHandler)
		if options.Conversations != nil {
			conversationsHandler := httpapi.NewConversationsHandler(options.Conversations)
			mux.Handle("/api/chat/sessions", conversationsHandler)
			mux.Handle("/api/chat/sessions/", conversationsHandler)
		}
		if options.History != nil {
			historyHandler := httpapi.NewHistoryHandler(options.History)
			mux.Handle("/api/codex/history", historyHandler)
			mux.Handle("/api/codex/history/", historyHandler)
		}
		if options.Scheduler != nil {
			mux.HandleFunc("GET /api/runtime", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				view := struct {
					codex.SchedulerSnapshot
					AppServer *codexapp.Health `json:"app_server,omitempty"`
				}{SchedulerSnapshot: options.Scheduler.Snapshot()}
				if options.AppServerHealth != nil {
					health := options.AppServerHealth()
					// Do not expose raw CLI diagnostics through the public runtime view.
					health.StderrTail = ""
					view.AppServer = &health
				}
				_ = json.NewEncoder(w).Encode(view)
			})
		}
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
	if options.AuthService == nil {
		return &App{handler: mux}
	}
	authHandler := httpapi.NewAuthHandler(options.AuthService)
	mux.Handle("/api/auth/", authHandler)
	protected := consoleauth.NewMiddleware(options.AuthService).Protect(mux)
	return &App{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health and login must remain reachable before a browser has a session.
		// The auth handler applies its own protection to all remaining auth routes.
		if r.URL.Path == "/api/health" || strings.HasPrefix(r.URL.Path, "/api/auth/") || !strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})}
}

type settingsWithScheduler struct {
	service   *consoleSettings.Service
	scheduler codex.Scheduler
}

func (s settingsWithScheduler) Get(ctx context.Context) (consoleSettings.View, error) {
	return s.service.Get(ctx)
}

func (s settingsWithScheduler) Update(ctx context.Context, public domain.PublicSettings, secrets map[string]string) (consoleSettings.View, error) {
	view, err := s.service.Update(ctx, public, secrets)
	if err != nil || s.scheduler == nil {
		return view, err
	}
	if err := s.scheduler.SetLimit(view.Public.MaxCodexConcurrency); err != nil {
		return consoleSettings.View{}, err
	}
	return view, nil
}

func (s settingsWithScheduler) TestDependency(ctx context.Context, dependency string) consoleSettings.Health {
	return s.service.TestDependency(ctx, dependency)
}

func (s settingsWithScheduler) RepairBaokuanMCP(ctx context.Context) consoleSettings.Health {
	return s.service.RepairBaokuanMCP(ctx)
}

// Handler returns the application's HTTP handler.
func (a *App) Handler() http.Handler {
	return a.handler
}
