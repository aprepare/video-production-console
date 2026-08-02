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
		root := filepath.Join(settings.DataRoot, "projects", task.ID)
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, "", err
		}
		cmd := exec.Command(settings.CodexBinaryPath, "exec", "--json", "--skip-git-repo-check", "-C", root, "-")
		cmd.Stdin = strings.NewReader(task.PromptSnapshot)
		return cmd, root, nil
	}
	makeResume := func(task domain.CodexTask, answer string) (*exec.Cmd, string, error) {
		if task.CodexSessionID == nil || *task.CodexSessionID == "" {
			return nil, "", fmt.Errorf("missing codex session")
		}
		root := filepath.Join(settings.DataRoot, "projects", task.ID)
		cmd := exec.Command(settings.CodexBinaryPath, "exec", "resume", *task.CodexSessionID, "--json", "-")
		cmd.Stdin = strings.NewReader(answer)
		return cmd, root, nil
	}
	scheduler, err := codex.NewScheduler(taskRepo, 2, makeCommand, makeResume, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer scheduler.Close()
	application := app.New(app.Options{Config: settings, DB: db, AssetService: assetService, Scheduler: scheduler, Obsidian: obsidian.New(settings.ObsidianVault)})
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
