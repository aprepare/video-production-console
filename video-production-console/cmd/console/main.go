package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"video-production-console/internal/app"
	"video-production-console/internal/assets"
	"video-production-console/internal/config"
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
	application := app.New(app.Options{Config: settings, DB: db, AssetService: assetService})
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
