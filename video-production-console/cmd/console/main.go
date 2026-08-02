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
	"video-production-console/internal/codex"
	"video-production-console/internal/config"
	"video-production-console/internal/domain"
	"video-production-console/internal/obsidian"
	"video-production-console/internal/realtime"
	"video-production-console/internal/store"
)

func main() {
	settings := config.Default()
	db, err := store.Open(settings.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	assetService := assets.NewService(settings.DataRoot)
	if err := assetService.ReconcileAccountBackgrounds(context.Background(), db, log.Default()); err != nil {
		log.Printf("account background reconciliation completed with errors: %v", err)
	}
	if err := assetService.ReconcileProjectAssets(context.Background(), db, log.Default()); err != nil {
		log.Printf("project asset reconciliation completed with errors: %v", err)
	}
	taskRepo := store.NewTaskRepository(db)
	makeCommand := func(task domain.CodexTask) (*exec.Cmd, string, error) {
		projectID := task.ID
		if task.ProjectID != nil && *task.ProjectID != "" {
			projectID = *task.ProjectID
		}
		root := filepath.Join(settings.DataRoot, "projects", projectID)
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, "", err
		}
		cmd := exec.Command(settings.CodexBinaryPath, "exec", "--json", "--skip-git-repo-check", "-C", root, "-")
		prompt := fmt.Sprintf("Use $%s for this isolated video project. Do not open WeChat Channels. Do not stop or restart the baokuan service. Write outputs only under %s.\n\nUser request:\n%s", task.SkillName, root, task.PromptSnapshot)
		cmd.Stdin = strings.NewReader(prompt)
		return cmd, root, nil
	}
	makeResume := func(task domain.CodexTask, answer string) (*exec.Cmd, string, error) {
		if task.CodexSessionID == nil || *task.CodexSessionID == "" {
			return nil, "", fmt.Errorf("missing codex session")
		}
		projectID := task.ID
		if task.ProjectID != nil && *task.ProjectID != "" {
			projectID = *task.ProjectID
		}
		root := filepath.Join(settings.DataRoot, "projects", projectID)
		cmd := exec.Command(settings.CodexBinaryPath, "exec", "resume", *task.CodexSessionID, "--json", "-")
		cmd.Stdin = strings.NewReader(answer)
		return cmd, root, nil
	}
	scheduler, err := codex.NewScheduler(taskRepo, 2, makeCommand, makeResume, nil)
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
	application := app.New(app.Options{Config: settings, DB: db, AssetService: assetService, Scheduler: scheduler, Realtime: hub, Obsidian: obsidian.New(settings.ObsidianVault)})
	log.Printf("video production console listening on %s", settings.ListenAddr)
	if err := newServer(settings.ListenAddr, application.Handler()).ListenAndServe(); err != nil {
		log.Fatal(err)
	}
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
