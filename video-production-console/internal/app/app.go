package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"video-production-console/internal/assets"
	consoleauth "video-production-console/internal/auth"
	"video-production-console/internal/baokuan"
	"video-production-console/internal/codex"
	"video-production-console/internal/codexapp"
	"video-production-console/internal/config"
	"video-production-console/internal/domain"
	"video-production-console/internal/httpapi"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/imagevideo"
	"video-production-console/internal/imagevideoruntime"
	"video-production-console/internal/logging"
	"video-production-console/internal/obsidian"
	"video-production-console/internal/realtime"
	"video-production-console/internal/remixlab"
	"video-production-console/internal/remixproducer"
	"video-production-console/internal/security"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/skillregistry"
	"video-production-console/internal/store"
	"video-production-console/internal/webui"
)

type remixLabRuntimeAdapter struct {
	inner *consoleSettings.Service
}

func (a remixLabRuntimeAdapter) Runtime(ctx context.Context) (remixlab.RuntimeView, error) {
	rt, err := a.inner.Runtime(ctx)
	if err != nil {
		return remixlab.RuntimeView{}, err
	}
	return remixlab.RuntimeView{
		RemixBaseURL:         rt.RemixBaseURL,
		RemixModel:           rt.RemixModel,
		RemixReasoningEffort: rt.RemixReasoningEffort,
		RemixAPIKey:          rt.RemixAPIKey,
		DataRoot:             rt.DataRoot,
		GrokBaseURL:          rt.GrokBaseURL,
		GrokModel:            rt.GrokModel,
		GrokAPIKey:           rt.GrokAPIKey,
	}, nil
}

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
	AppServerHealth func() codexapp.Health
	MontageRetryer  interface {
		Retry(context.Context, string) (domain.RegistrationAttempt, error)
	}
	CompletionRetryer httpapi.CompletionRetryer
	DesktopOpener     assets.DesktopOpener
	RemixCoordinator  httpapi.RemixCoordinator
	// MediaCatalog overrides the default settings-backed catalog service;
	// tests inject fakes through it.
	MediaCatalog      httpapi.CatalogService
	ImageVideoService *imagevideo.Service
	ImageVideoStarter interface {
		Kick(context.Context, string) error
	}
	ImageVideoCloser interface{ Close() }
	// Restart spawns a replacement console process and schedules a graceful
	// shutdown of this one. Nil disables POST /api/system/restart.
	Restart func() error
	// InternalToken 是本进程服务间调用的旁路令牌（生产驱动器回环调用自身
	// API 时携带 X-Internal-Token）。空则不启用旁路。
	InternalToken string
}

// App is the HTTP application.
type App struct {
	handler http.Handler
	close   func()
	// resumeProductions 重启后续跑生产段（监听起来后调用）。
	resumeProductions func()
}

// ResumeProductions 在 HTTP 监听就绪后调用：把重启前进行到一半的生产接着跑。
func (a *App) ResumeProductions() {
	if a.resumeProductions != nil {
		a.resumeProductions()
	}
}

// New constructs the application and its routes.
func New(options Options) *App {
	var resumeProductions func()
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
		var models httpapi.TaskModelResolver
		if options.Settings != nil {
			models = options.Settings
		}
		projects := httpapi.NewProjectsHandler(options.DB, assetService, options.RemixCoordinator, models)
		mux.Handle("/api/projects", projects)
		assetOptions := httpapi.AssetHandlerOptions{DesktopOpener: options.DesktopOpener}
		if options.Settings != nil {
			assetOptions.Runtime = options.Settings
		}
		assetsHandler := httpapi.NewAssetsHandler(options.DB, assetService, assetOptions)
		mux.Handle("/api/assets/", assetsHandler)
		tasksHandler := httpapi.NewTasksHandler(options.DB, options.Scheduler, options.TaskPreparer, models)
		taskResultsHandler := httpapi.NewTaskResultsHandler(store.NewTaskRepository(options.DB))
		montageHandler := httpapi.NewMontageHandler(options.MontageRetryer)
		completionRetryHandler := httpapi.NewCompletionRetryHandler(options.CompletionRetryer)
		narrationOptions := httpapi.NarrationHandlerOptions{}
		if options.Settings != nil {
			narrationOptions.Runtime = options.Settings
			narrationOptions.Produce = httpapi.NewVolcengineProducer(options.Settings)
		}
		narrationHandler := httpapi.NewNarrationHandler(options.DB, assetService, narrationOptions)
		mux.Handle("/api/projects/", projectRouteHandler(projects, tasksHandler, narrationHandler, options.Scheduler != nil))
		tasks := store.NewTaskRepository(options.DB)
		hub := options.Realtime
		if hub == nil {
			hub = realtime.NewHub(tasks)
		}
		mux.Handle("/api/tasks/", taskRouteHandler(tasksHandler, taskResultsHandler, montageHandler, completionRetryHandler, hub))
		// Mount task commands separately; the event route above remains the
		// narrowly-scoped WebSocket endpoint.
		if options.Scheduler != nil {
			mux.Handle("/api/tasks", tasksHandler)
		}
		if options.Settings != nil {
			mux.Handle("/api/settings", httpapi.NewSettingsHandler(settingsWithScheduler{service: options.Settings, scheduler: options.Scheduler}))
			mux.Handle("/api/settings/", httpapi.NewSettingsHandler(settingsWithScheduler{service: options.Settings, scheduler: options.Scheduler}))
			imageVideoService := options.ImageVideoService
			if imageVideoService == nil {
				imageVideoService = imagevideo.NewService(store.NewImageProjectRepository(options.DB), store.NewImageVideoJobRepository(options.DB), imagevideoruntime.Provider{Inner: options.Settings}, nil)
			}
			imageProjectsHandler := httpapi.NewImageProjectsHandlerWithImageVideo(options.DB, options.Settings, imageproject.NewClient(nil), imageproject.NewHTTPChatClient(nil), imageVideoService, options.ImageVideoStarter)
			mux.Handle("/api/image-projects", imageProjectsHandler)
			mux.Handle("/api/image-projects/", imageProjectsHandler)
			mux.Handle("/api/image-videos", imageProjectsHandler)
			mux.Handle("/api/image-videos/", imageProjectsHandler)
			mux.Handle("/api/image-video-jobs/", httpapi.NewImageVideoJobsHandler(options.DB, options.Settings, options.ImageVideoStarter, imageVideoService))
			catalogService := options.MediaCatalog
			if catalogService == nil {
				catalogService = newMediaCatalogService(options.Settings)
			}
			mux.Handle("/api/media-catalog/", httpapi.NewMediaCatalogHandler(catalogService))
			bgmHandler := httpapi.NewBGMLibraryHandler(options.Settings)
			mux.Handle("/api/bgm-library", bgmHandler)
			mux.Handle("/api/bgm-library/", bgmHandler)
		}
		if options.Skills != nil {
			skillsHandler := httpapi.NewSkillsHandler(options.Skills)
			mux.Handle("/api/skills", skillsHandler)
			mux.Handle("/api/skills/", skillsHandler)
		}
		ideasHandler := httpapi.NewIdeasHandler(options.DB, options.Scheduler, options.TaskPreparer, models)
		mux.Handle("/api/ideas", ideasHandler)
		mux.Handle("/api/ideas/", ideasHandler)
		if options.Settings != nil {
			projectRepo := store.NewProjectRepository(options.DB)
			remixLabSvc := remixlab.NewService(
				store.NewRemixLabRepository(options.DB),
				remixLabRuntimeAdapter{inner: options.Settings},
				security.NewSecretProtector(),
				options.Config.DataRoot,
				nil,
				assetService,
				projectRepo,
			)
			if err := remixLabSvc.FailStale(context.Background()); err != nil {
				slog.Error("remix lab stale runs", "error", err)
			}
			// 生产驱动器：确认闸门放行后回环调用自身 API 串起混剪链路。
			if options.InternalToken != "" {
				producer := remixproducer.New(store.NewRemixLabRepository(options.DB), options.Config.ListenAddr, options.InternalToken)
				remixLabSvc.SetProducer(producer)
				resumeProductions = producer.ResumeAll
			}
			mux.Handle("/api/remix-lab/", httpapi.NewRemixLabHandler(remixLabSvc, projectRepo))
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
	if options.Restart != nil {
		mux.HandleFunc("POST /api/system/restart", func(w http.ResponseWriter, _ *http.Request) {
			if err := options.Restart(); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "restart_failed", "message": "控制台重启失败，请手动重启。"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "restarting"})
		})
	}
	mux.HandleFunc("GET /api/obsidian", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(options.Obsidian.Health())
	})
	mux.Handle("/", webui.Handler())
	if options.AuthService == nil {
		return &App{handler: logging.RequestID(mux), close: closeImageVideo(options.ImageVideoCloser), resumeProductions: resumeProductions}
	}
	authHandler := httpapi.NewAuthHandler(options.AuthService)
	mux.Handle("/api/auth/", authHandler)
	protected := consoleauth.NewMiddleware(options.AuthService).Protect(mux)
	internalToken := options.InternalToken
	return &App{handler: logging.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health and login must remain reachable before a browser has a session.
		// The auth handler applies its own protection to all remaining auth routes.
		if r.URL.Path == "/api/health" || strings.HasPrefix(r.URL.Path, "/api/auth/") || !strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		// 本进程服务间旁路：仅回环地址 + 每次启动随机生成的令牌，供生产
		// 驱动器调用自身 API；不开放给外部请求。
		if internalToken != "" && r.Header.Get("X-Internal-Token") == internalToken && isLoopbackRemote(r.RemoteAddr) {
			mux.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})), close: closeImageVideo(options.ImageVideoCloser), resumeProductions: resumeProductions}
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func closeImageVideo(closer interface{ Close() }) func() {
	if closer == nil {
		return func() {}
	}
	return closer.Close
}

func projectRouteHandler(projects, tasks, narration http.Handler, taskRoutesEnabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if taskRoutesEnabled && (strings.HasSuffix(r.URL.Path, "/tasks") || strings.HasSuffix(r.URL.Path, "/topic-card")) {
			tasks.ServeHTTP(w, r)
			return
		}
		if narration != nil && strings.HasSuffix(r.URL.Path, "/narration") && !strings.Contains(r.URL.Path, "/assets/") {
			narration.ServeHTTP(w, r)
			return
		}
		projects.ServeHTTP(w, r)
	})
}

func taskRouteHandler(tasks, results, montage, completion http.Handler, hub *realtime.Hub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/artifacts"), strings.HasSuffix(path, "/result"), strings.HasSuffix(path, "/diagnostics"), strings.HasSuffix(path, "/semantic-events"):
			results.ServeHTTP(w, r)
		case strings.HasSuffix(path, "/retry-registration"):
			montage.ServeHTTP(w, r)
		case strings.HasSuffix(path, "/retry-completion"):
			completion.ServeHTTP(w, r)
		case strings.HasSuffix(path, "/events"):
			taskID := strings.TrimSuffix(strings.TrimPrefix(path, "/api/tasks/"), "/events")
			if taskID == "" || strings.Contains(taskID, "/") {
				http.NotFound(w, r)
				return
			}
			hub.Handler(w, r, taskID)
		default:
			tasks.ServeHTTP(w, r)
		}
	})
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

func (a *App) Close() {
	if a != nil && a.close != nil {
		a.close()
	}
}
