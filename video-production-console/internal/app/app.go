package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"video-production-console/internal/buildinfo"
	"video-production-console/internal/mcpserver"

	"video-production-console/internal/aigc"
	"video-production-console/internal/assets"
	consoleauth "video-production-console/internal/auth"
	"video-production-console/internal/baokuan"
	"video-production-console/internal/codex"
	"video-production-console/internal/codexapp"
	"video-production-console/internal/commentinsight"
	"video-production-console/internal/config"
	"video-production-console/internal/domain"
	"video-production-console/internal/httpapi"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/imagevideo"
	"video-production-console/internal/imagevideoruntime"
	"video-production-console/internal/logging"
	"video-production-console/internal/monitor"
	"video-production-console/internal/obsidian"
	"video-production-console/internal/realtime"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/references"
	"video-production-console/internal/remixlab"
	"video-production-console/internal/remixproducer"
	"video-production-console/internal/replicate"
	"video-production-console/internal/security"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/setup"
	"video-production-console/internal/skillregistry"
	"video-production-console/internal/store"
	"video-production-console/internal/videoqc"
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
		CoverSkillRoot:       rt.CoverSkillRoot,
		CoverOutputDir:       rt.CoverOutputDir,
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
	// background holds long-running loops (对标转写轮询) started by
	// RunBackground once the server is up; they stop with ctx.
	background []func(context.Context)
}

// RunBackground starts the application's long-running loops; each returns
// when ctx is cancelled.
func (a *App) RunBackground(ctx context.Context) {
	for _, run := range a.background {
		go run(ctx)
	}
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
	var background []func(context.Context)
	closers := []func(){closeImageVideo(options.ImageVideoCloser)}
	closeServices := func() {
		for _, closeService := range closers {
			closeService()
		}
	}
	mux := http.NewServeMux()
	removedFeature := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "feature_removed", "message": "此功能已移除。"})
	})
	for _, path := range []string{"/api/workspace", "/api/workspace/", "/api/ai-shorts", "/api/ai-shorts/"} {
		mux.Handle(path, removedFeature)
	}
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
			setupHandler := httpapi.NewSetupHandler(setup.NewService(options.Settings, setup.Options{}), options.Settings)
			mux.Handle("/api/setup/", setupHandler)
			mux.Handle("/api/jianying-drafts/detect", setupHandler)
			mux.Handle("/api/jianying-drafts/detect/", setupHandler)
			imageVideoService := options.ImageVideoService
			if imageVideoService == nil {
				imageVideoService = imagevideo.NewService(store.NewImageProjectRepository(options.DB), store.NewImageVideoJobRepository(options.DB), imagevideoruntime.Provider{Inner: options.Settings}, nil)
			}
			imageProjectsHandler := httpapi.NewImageProjectsHandlerWithImageVideo(options.DB, options.Settings, imageproject.NewClient(nil), imageproject.NewHTTPChatClient(nil), imageVideoService, options.ImageVideoStarter)
			mux.Handle("/api/image-projects", imageProjectsHandler)
			mux.Handle("/api/image-projects/", imageProjectsHandler)
			imageStudioHandler := httpapi.NewImageStudioHandler(options.DB, options.Settings)
			mux.Handle("/api/image-studio", imageStudioHandler)
			mux.Handle("/api/image-studio/", imageStudioHandler)
			closers = append(closers, imageStudioHandler.Close)
			mux.Handle("/api/image-videos", imageProjectsHandler)
			mux.Handle("/api/image-videos/", imageProjectsHandler)
			// The image-projects handler already serves the job routes; mounting
			// the same instance under the job prefix keeps one repository, one
			// starter and one in-flight job table instead of two parallel ones.
			mux.Handle("/api/image-video-jobs/", imageProjectsHandler)
			catalogService := options.MediaCatalog
			if catalogService == nil {
				catalogService = newMediaCatalogService(options.Settings)
			}
			mux.Handle("/api/media-catalog/", httpapi.NewMediaCatalogHandler(catalogService))
			bgmHandler := httpapi.NewBGMLibraryHandler(options.Settings)
			mux.Handle("/api/bgm-library", bgmHandler)
			mux.Handle("/api/bgm-library/", bgmHandler)
			draftsHandler := httpapi.NewJianyingDraftsHandler(options.Settings, options.DB)
			mux.Handle("/api/jianying-drafts", draftsHandler)
			mux.Handle("/api/jianying-drafts/", draftsHandler)
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
			existingAccounts, migrationErr := store.NewAccountRepository(options.DB).List(context.Background())
			if migrationErr == nil {
				ids := make([]string, 0, len(existingAccounts))
				for _, account := range existingAccounts {
					ids = append(ids, account.ID)
				}
				migrationErr = (remixlab.Store{DataRoot: options.Config.DataRoot}).MigrateExistingAccountWorkflows(ids)
			}
			remixLabSvc := remixlab.NewService(
				store.NewRemixLabRepository(options.DB),
				remixLabRuntimeAdapter{inner: options.Settings},
				security.NewSecretProtector(),
				options.Config.DataRoot,
				nil,
				assetService,
				projectRepo,
			)
			if script := rhStudioScript(); script != "" {
				remixLabSvc.SetAICoverRunner(remixlab.RHScriptRunner("python", script))
			}
			if err := remixLabSvc.FailStale(context.Background()); err != nil {
				slog.Error("remix lab stale runs", "error", err)
			}
			// 生产驱动器：确认闸门放行后回环调用自身 API 串起混剪链路。
			if options.InternalToken != "" && migrationErr == nil {
				producer := remixproducer.New(store.NewRemixLabRepository(options.DB), options.Config.ListenAddr, options.InternalToken)
				remixLabSvc.SetProducer(producer)
				resumeProductions = producer.ResumeAll
			}
			if migrationErr != nil {
				slog.Error("migrate remix workflows", "error", migrationErr)
				mux.HandleFunc("/api/remix-lab/", func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					_ = json.NewEncoder(w).Encode(map[string]string{"code": "workflow_migration_failed", "message": "账号工作流迁移失败，请检查服务日志后重启。"})
				})
			} else {
				mux.Handle("/api/remix-lab/", httpapi.NewRemixLabHandler(remixLabSvc, projectRepo, store.NewTaskRepository(options.DB)))
			}

			referenceRepo := store.NewReferenceRepository(options.DB)
			referenceService := references.New(referenceRepo, options.Settings)
			referencesHandler := httpapi.NewReferencesHandler(referenceService, referenceRepo)
			mux.Handle("/api/references", referencesHandler)
			mux.Handle("/api/references/", referencesHandler)
			background = append(background, referenceService.Run)

			analysisService := refanalysis.New(store.NewReferenceAnalysisRepository(options.DB), referenceRepo, options.Settings)
			resumeCtx, cancelResume := context.WithTimeout(context.Background(), 10*time.Second)
			if err := analysisService.Resume(resumeCtx); err != nil {
				slog.Error("恢复对标拆解状态失败")
			}
			cancelResume()
			analysesHandler := httpapi.NewReferenceAnalysesHandler(analysisService)
			mux.Handle("/api/reference-analyses", analysesHandler)
			mux.Handle("/api/reference-analyses/", analysesHandler)

			replicateService, replicateErr := replicate.New(store.NewReplicateRepository(options.DB), store.NewReferenceAnalysisRepository(options.DB), options.Settings, replicate.Options{ReferenceFrame: analysisService.OpenFrame})
			qcOptions := videoqc.Options{}
			if replicateErr == nil {
				qcOptions.ReplicateSource = replicateService.QCSource
			} else {
				slog.Error("初始化复刻工作台失败")
			}
			qcService := videoqc.New(store.NewVideoQCRepository(options.DB), referenceRepo, options.Settings, qcOptions)
			resumeCtx, cancelResume = context.WithTimeout(context.Background(), 10*time.Second)
			if err := qcService.Resume(resumeCtx); err != nil {
				slog.Error("恢复视频审核状态失败")
			}
			cancelResume()
			qcHandler := httpapi.NewVideoQCHandler(qcService)
			mux.Handle("/api/video-qc", qcHandler)
			mux.Handle("/api/video-qc/", qcHandler)
			closers = append(closers, qcService.Close)
			if replicateService != nil {
				replicateService.SetQC(qcService)
				resumeCtx, cancelResume = context.WithTimeout(context.Background(), 10*time.Second)
				if err := replicateService.Resume(resumeCtx); err != nil {
					slog.Error("恢复复刻工作台状态失败")
				}
				cancelResume()
				replicateHandler := httpapi.NewReplicateHandler(replicateService)
				mux.Handle("/api/replicate", replicateHandler)
				mux.Handle("/api/replicate/", replicateHandler)
				closers = append(closers, replicateService.Close)
			}

			insightService := commentinsight.New(store.NewCommentInsightRepository(options.DB), referenceRepo, options.Settings, referenceService)
			monitorService := monitor.New(store.NewMonitorRepository(options.DB), referenceService, referenceRepo, options.Settings, monitor.Options{
				Analyze: func(ctx context.Context, referenceID, model string) error {
					_, err := analysisService.Start(ctx, referenceID, model, "")
					return err
				},
				Insight: func(ctx context.Context, referenceID, model string) error {
					_, _, err := insightService.Start(ctx, referenceID, model)
					return err
				},
			})
			resumeCtx, cancelResume = context.WithTimeout(context.Background(), 10*time.Second)
			if err := insightService.Resume(resumeCtx); err != nil {
				slog.Error("恢复评论洞察状态失败")
			}
			if err := monitorService.Resume(resumeCtx); err != nil {
				slog.Error("恢复监控任务状态失败")
			}
			cancelResume()
			monitorsHandler := httpapi.NewMonitorsHandler(monitorService)
			mux.Handle("/api/monitors", monitorsHandler)
			mux.Handle("/api/monitors/", monitorsHandler)
			insightsHandler := httpapi.NewCommentInsightsHandler(insightService)
			mux.Handle("/api/comment-insights", insightsHandler)
			mux.Handle("/api/comment-insights/", insightsHandler)
			background = append(background, monitorService.Run)
			closers = append(closers, func() {
				monitorService.Close()
				insightService.Close()
				analysisService.Close()
			})

			merchantsHandler := httpapi.NewMerchantsHandler(options.DB, options.Config.DataRoot)
			for _, prefix := range []string{"/api/merchants", "/api/merchants/", "/api/materials/", "/api/orders", "/api/orders/", "/api/order-costs/", "/api/business/"} {
				mux.Handle(prefix, merchantsHandler)
			}
		}
		var aigcRuntime aigc.RuntimeProvider
		if options.Settings != nil {
			aigcRuntime = options.Settings
		}
		aigcService := aigc.New(options.DB, options.Config.DataRoot, aigcRuntime)
		resumeCtx, cancelResume := context.WithTimeout(context.Background(), 10*time.Second)
		if err := aigcService.Resume(resumeCtx); err != nil {
			slog.Error("resume AIGC jobs", "error", err)
		}
		cancelResume()
		mux.Handle("/api/aigc/", httpapi.NewAIGCHandler(aigcService))

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
		return &App{handler: logging.RequestID(mux), close: closeServices, resumeProductions: resumeProductions, background: background}
	}
	authHandler := httpapi.NewAuthHandler(options.AuthService)
	mux.Handle("/api/auth/", authHandler)
	protected := consoleauth.NewMiddleware(options.AuthService).Protect(mux)
	internalToken := options.InternalToken
	var gateway http.Handler
	gateway = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})
	handler := gateway
	if internalToken != "" {
		tokens := mcpserver.NewTokenStore(options.Config.DataRoot)
		mcp := mcpserver.New(gateway, internalToken, tokens.Current, buildinfo.String())
		mux.Handle("GET /api/mcp/token", mcpTokenHandler(tokens, options.Config.ListenAddr, false))
		mux.Handle("POST /api/mcp/token/rotate", mcpTokenHandler(tokens, options.Config.ListenAddr, true))
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/mcp" || r.URL.Path == "/mcp/" {
				mcp.ServeHTTP(w, r)
				return
			}
			gateway.ServeHTTP(w, r)
		})
	}
	return &App{handler: logging.RequestID(handler), close: closeServices, resumeProductions: resumeProductions, background: background}
}

// mcpTokenHandler 给已登录的操作员看 MCP 令牌和接入配置；AI 走内部令牌调用时拒绝，免得它自己换令牌。
func mcpTokenHandler(tokens *mcpserver.TokenStore, listenAddr string, rotate bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		token := tokens.Current()
		if rotate {
			var err error
			if token, err = tokens.Rotate(); err != nil {
				http.Error(w, "令牌重置失败", http.StatusInternalServerError)
				return
			}
		}
		if token == "" {
			http.Error(w, "令牌生成失败", http.StatusInternalServerError)
			return
		}
		endpoint := "http://" + mcpHost(listenAddr) + "/mcp"
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token":    token,
			"endpoint": endpoint,
			"claude":   `claude mcp add --transport http jiying-console ` + endpoint + ` --header "Authorization: Bearer ` + token + `"`,
			"codex":    "[mcp_servers.jiying-console]\nurl = \"" + endpoint + "\"\nhttp_headers = { Authorization = \"Bearer " + token + "\" }",
			"json":     `{"mcpServers":{"jiying-console":{"type":"http","url":"` + endpoint + `","headers":{"Authorization":"Bearer ` + token + `"}}}}`,
		})
	})
}

func mcpHost(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "127.0.0.1:2030"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
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

// rhStudioScript 找 rh-studio 的 rh.py（RunningHub 出图）；找不到时 AI 封面不可用。
func rhStudioScript() string {
	candidate := strings.TrimSpace(os.Getenv("VIDEO_CONSOLE_RH_SCRIPT"))
	if candidate == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		candidate = filepath.Join(home, ".claude", "skills", "rh-studio", "scripts", "rh.py")
	}
	if info, err := os.Stat(candidate); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return candidate
}
