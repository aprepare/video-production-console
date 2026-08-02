package main

import (
	"log"
	"net/http"

	"video-production-console/internal/app"
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
	application := app.New(app.Options{Config: settings, DB: db})
	log.Printf("video production console listening on %s", settings.ListenAddr)
	if err := http.ListenAndServe(settings.ListenAddr, application.Handler()); err != nil {
		log.Fatal(err)
	}
}
