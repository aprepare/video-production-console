package main

import (
	"log"
	"net/http"

	"video-production-console/internal/app"
	"video-production-console/internal/config"
)

func main() {
	settings := config.Default()
	application := app.New(app.Options{Config: settings})
	log.Printf("video production console listening on %s", settings.ListenAddr)
	if err := http.ListenAndServe(settings.ListenAddr, application.Handler()); err != nil {
		log.Fatal(err)
	}
}
