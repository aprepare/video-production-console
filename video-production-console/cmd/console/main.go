package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"video-production-console/internal/app"
	"video-production-console/internal/assets"
	consoleauth "video-production-console/internal/auth"
	"video-production-console/internal/codex"
	"video-production-console/internal/config"
	"video-production-console/internal/domain"
	"video-production-console/internal/httpapi"
	"video-production-console/internal/obsidian"
	"video-production-console/internal/realtime"
	"video-production-console/internal/security"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/skillregistry"
	"video-production-console/internal/store"
)

var codexSecretEnvironmentKeys = []string{
	"GROK_SEARCH_BASE_URL",
	"GROK_SEARCH_MODEL",
	"GROK_SEARCH_API_KEY",
	"PEXELS_API_KEY",
}

func main() {
	settings := config.Default()
	settings.DataRoot = absolutePath(settings.DataRoot)
	settings.DatabasePath = filepath.Join(settings.DataRoot, "console.db")
	codexPath, err := exec.LookPath(settings.CodexBinaryPath)
	if err != nil {
		log.Fatalf("resolve Codex binary: %v", err)
	}
	settings.CodexBinaryPath, err = filepath.Abs(codexPath)
	if err != nil {
		log.Fatal(err)
	}
	if settings.ObsidianVault != "" {
		settings.ObsidianVault = absolutePath(settings.ObsidianVault)
	}
	executablePath, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	developmentRoot, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	schemaPath, err := resolveResultSchemaPath(executablePath, developmentRoot)
	if err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(settings.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	settingsService := consoleSettings.NewService(store.NewSettingsRepository(db), security.NewSecretProtector())
	boot := consoleSettings.BootSettings{ListenAddr: settings.ListenAddr, DataRoot: settings.DataRoot, CodexBinaryPath: settings.CodexBinaryPath, ObsidianVault: settings.ObsidianVault}
	if err := settingsService.InitializeBootSettings(context.Background(), boot); err != nil {
		log.Fatalf("initialize settings: %v", err)
	}
	runtimeSettings, err := settingsService.Runtime(context.Background())
	if err != nil {
		log.Fatalf("load runtime settings: %v", err)
	}
	settings.ListenAddr = runtimeSettings.ListenAddr
	settings.DataRoot = runtimeSettings.DataRoot
	settings.BaokuanBaseURL = runtimeSettings.BaokuanBaseURL
	settings.CodexBinaryPath = runtimeSettings.CodexBinaryPath
	settings.ObsidianVault = runtimeSettings.ObsidianVault
	commandConfig := codex.Config{CodexBinaryPath: settings.CodexBinaryPath, ResultSchema: schemaPath, SecretEnvironment: runtimeSecretEnvironment(runtimeSettings, os.LookupEnv), Redactor: security.NewRedactor()}
	assetService := assets.NewService(settings.DataRoot)
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		log.Fatalf("resolve user home for Skills: %v", homeErr)
	}
	skillsService := skillregistry.NewService(store.NewSkillRepository(db), skillregistry.Options{
		Roots: skillregistry.DefaultRoots(filepath.Join(home, ".codex", "skills")),
	})
	if _, scanErr := skillsService.ScanAll(context.Background()); scanErr != nil {
		log.Printf("Skill scan completed with errors; manifest-backed tasks may be unavailable: %v", scanErr)
	}
	taskPreparer := httpapi.NewTaskManifestPreparer(db, settingsService, skillsService)
	if err := assetService.ReconcileAccountBackgrounds(context.Background(), db, log.Default()); err != nil {
		log.Printf("account background reconciliation completed with errors: %v", err)
	}
	if err := assetService.ReconcileProjectAssets(context.Background(), db, log.Default()); err != nil {
		log.Printf("project asset reconciliation completed with errors: %v", err)
	}
	taskRepo := store.NewTaskRepository(db)
	if interrupted, err := taskRepo.InterruptInFlight(context.Background()); err != nil {
		log.Fatalf("recover interrupted tasks: %v", err)
	} else if interrupted > 0 {
		log.Printf("marked %d unfinished Codex task(s) interrupted after restart", interrupted)
	}
	makeCommand, makeResume := newCodexCommandFactories(settings, commandConfig)
	scheduler, err := codex.NewScheduler(taskRepo, runtimeSettings.MaxCodexConcurrency, makeCommand, makeResume, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer scheduler.Close()
	hub := realtime.NewHub(taskRepo)
	scheduler.SetTaskBroadcast(func(taskID string, _ codex.Event) {
		events, err := taskRepo.Events(context.Background(), taskID)
		if err == nil && len(events) > 0 {
			hub.Publish(context.Background(), taskID, events[len(events)-1])
		}
	})
	authService := consoleauth.NewService(store.NewAuthStore(db), consoleauth.Options{})
	if err := authService.Bootstrap(context.Background(), "123321"); err != nil {
		log.Fatalf("bootstrap administrator: %v", err)
	}
	application := app.New(app.Options{Config: settings, DB: db, AssetService: assetService, Scheduler: scheduler, Realtime: hub, Obsidian: obsidian.New(settings.ObsidianVault), AuthService: authService, Settings: settingsService, Skills: skillsService, TaskPreparer: taskPreparer})
	log.Printf("video production console listening on %s", settings.ListenAddr)
	if err := newServer(settings.ListenAddr, application.Handler()).ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func absolutePath(value string) string {
	if value == "" || filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	resolved, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(value)
	}
	return resolved
}

func resolveResultSchemaPath(executablePath, developmentRoot string) (string, error) {
	if strings.TrimSpace(executablePath) == "" || !filepath.IsAbs(executablePath) {
		return "", fmt.Errorf("executable path must be absolute")
	}
	resolvedExecutable, err := filepath.EvalSymlinks(executablePath)
	if err != nil {
		return "", fmt.Errorf("canonicalize executable path: %w", err)
	}
	info, err := os.Stat(resolvedExecutable)
	if err != nil {
		return "", fmt.Errorf("inspect executable path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("executable path is not a regular file")
	}
	executableDirectory := filepath.Dir(resolvedExecutable)
	roots := []string{executableDirectory, filepath.Dir(executableDirectory)}
	if developmentRoot != "" {
		if !filepath.IsAbs(developmentRoot) {
			return "", fmt.Errorf("development root must be absolute")
		}
		roots = append(roots, developmentRoot)
	}

	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", fmt.Errorf("canonicalize schema root %q: %w", root, err)
		}
		key := strings.ToLower(filepath.Clean(canonicalRoot))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidate := filepath.Join(canonicalRoot, "schemas", "codex-result.schema.json")
		candidateInfo, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect result schema %q: %w", candidate, err)
		}
		if !candidateInfo.Mode().IsRegular() {
			return "", fmt.Errorf("result schema %q is not a regular file", candidate)
		}
		resolvedCandidate, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("canonicalize result schema %q: %w", candidate, err)
		}
		if !pathWithinRoot(canonicalRoot, resolvedCandidate) {
			return "", fmt.Errorf("result schema %q escaped controlled root %q", resolvedCandidate, canonicalRoot)
		}
		return resolvedCandidate, nil
	}
	return "", fmt.Errorf("codex result schema was not found under controlled install roots or the explicit development root")
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func newCodexCommandFactories(settings config.Config, base codex.Config) (codex.CommandFactory, codex.ResumeCommandFactory) {
	makeCommand := func(task domain.CodexTask) (*exec.Cmd, string, error) {
		root, workspace, err := managedTaskRoot(settings.DataRoot, task)
		if err != nil {
			return nil, "", err
		}
		taskRoot, err := managedTaskDirectory(root, task.ID)
		if err != nil {
			return nil, "", err
		}
		cfg := taskCommandConfig(base, root, taskRoot)
		ctx := codex.TaskContext{
			ProjectID:    projectIDForTask(task),
			TaskType:     task.Type,
			WorkspaceDir: workspace,
			ProjectDir:   root,
			AllowedDir:   taskRoot,
		}
		// A task manifest is optional while older HTTP task creation has not
		// supplied the asset versions required by every V2 action. When a
		// manifest has been prepared in this controlled task directory, make it
		// authoritative for the command prompt rather than falling back to the
		// compatibility task-type mapping.
		manifestPath := filepath.Join(taskRoot, "task_manifest.json")
		if info, statErr := os.Lstat(manifestPath); statErr == nil {
			if !info.Mode().IsRegular() {
				return nil, "", fmt.Errorf("task manifest must be a regular file")
			}
			ctx.ManifestPath = manifestPath
		} else if !os.IsNotExist(statErr) {
			return nil, "", fmt.Errorf("inspect task manifest: %w", statErr)
		}
		guard, err := codex.OpenProjectDirGuard(ctx)
		if err != nil {
			return nil, "", err
		}
		ctx.ProjectDirGuard = guard
		cmd, buildErr := codex.BuildExecCommand(cfg, ctx)
		closeErr := guard.Close()
		if buildErr != nil {
			return nil, "", buildErr
		}
		if closeErr != nil {
			return nil, "", fmt.Errorf("release project directory guard: %w", closeErr)
		}
		return cmd, root, nil
	}
	makeResume := func(task domain.CodexTask, answer string) (*exec.Cmd, string, error) {
		if task.CodexSessionID == nil {
			return nil, "", fmt.Errorf("missing codex session")
		}
		root, _, err := managedTaskRoot(settings.DataRoot, task)
		if err != nil {
			return nil, "", err
		}
		taskRoot, err := managedTaskDirectory(root, task.ID)
		if err != nil {
			return nil, "", err
		}
		cmd, err := codex.BuildResumeCommand(taskCommandConfig(base, root, taskRoot), *task.CodexSessionID, answer)
		if err != nil {
			return nil, "", err
		}
		return cmd, root, nil
	}
	return makeCommand, makeResume
}

func taskCommandConfig(base codex.Config, projectRoot, taskRoot string) codex.Config {
	base.WorkingDirectory = projectRoot
	base.OutputLastMessage = filepath.Join(taskRoot, "output-last-message.json")
	return base
}

func projectIDForTask(task domain.CodexTask) string {
	if task.ProjectID != nil && *task.ProjectID != "" {
		return *task.ProjectID
	}
	return task.ID
}

func managedTaskRoot(dataRoot string, task domain.CodexTask) (string, string, error) {
	projectID := projectIDForTask(task)
	if !validManagedID(projectID) {
		return "", "", fmt.Errorf("invalid managed project id")
	}
	workspace, err := filepath.Abs(filepath.Clean(dataRoot))
	if err != nil {
		return "", "", fmt.Errorf("normalize data root: %w", err)
	}
	projectsRoot := filepath.Join(workspace, "projects")
	if err := os.MkdirAll(projectsRoot, 0o755); err != nil {
		return "", "", err
	}
	projectsRoot, err = filepath.EvalSymlinks(projectsRoot)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize projects root: %w", err)
	}
	root := filepath.Join(projectsRoot, projectID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize project root: %w", err)
	}
	rel, err := filepath.Rel(projectsRoot, root)
	if err != nil || rel != projectID {
		return "", "", fmt.Errorf("managed project root escaped projects boundary")
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize data root: %w", err)
	}
	return root, workspace, nil
}

func managedTaskDirectory(projectRoot, taskID string) (string, error) {
	if !validManagedID(taskID) {
		return "", fmt.Errorf("invalid managed task id")
	}
	tasksRoot := filepath.Join(projectRoot, "tasks")
	if err := os.MkdirAll(tasksRoot, 0o700); err != nil {
		return "", err
	}
	tasksRoot, err := filepath.EvalSymlinks(tasksRoot)
	if err != nil {
		return "", fmt.Errorf("canonicalize tasks root: %w", err)
	}
	if rel, err := filepath.Rel(projectRoot, tasksRoot); err != nil || rel != "tasks" {
		return "", fmt.Errorf("managed tasks root escaped project boundary")
	}
	taskRoot := filepath.Join(tasksRoot, taskID)
	if err := os.MkdirAll(taskRoot, 0o700); err != nil {
		return "", err
	}
	taskRoot, err = filepath.EvalSymlinks(taskRoot)
	if err != nil {
		return "", fmt.Errorf("canonicalize task root: %w", err)
	}
	wantRel := filepath.Join("tasks", taskID)
	if rel, err := filepath.Rel(projectRoot, taskRoot); err != nil || rel != wantRel {
		return "", fmt.Errorf("managed task root escaped project boundary")
	}
	return taskRoot, nil
}

func validManagedID(id string) bool {
	return id != "" && strings.TrimSpace(id) == id && !filepath.IsAbs(id) && filepath.Base(id) == id && id != "." && id != ".."
}

func loadCodexSecretEnvironment(lookup func(string) (string, bool)) map[string]string {
	environment := make(map[string]string, len(codexSecretEnvironmentKeys))
	for _, key := range codexSecretEnvironmentKeys {
		if value, ok := lookup(key); ok {
			environment[key] = value
		}
	}
	return environment
}

func runtimeSecretEnvironment(runtime consoleSettings.Runtime, lookup func(string) (string, bool)) map[string]string {
	environment := loadCodexSecretEnvironment(lookup)
	if runtime.GrokAPIKey != "" {
		environment["GROK_SEARCH_API_KEY"] = runtime.GrokAPIKey
	}
	if runtime.GrokBaseURL != "" {
		environment["GROK_SEARCH_BASE_URL"] = runtime.GrokBaseURL
	}
	if runtime.GrokModel != "" {
		environment["GROK_SEARCH_MODEL"] = runtime.GrokModel
	}
	if runtime.PexelsAPIKey != "" {
		environment["PEXELS_API_KEY"] = runtime.PexelsAPIKey
	}
	return environment
}

func newServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
}
