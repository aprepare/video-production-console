package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist/* dist/assets/*
var files embed.FS

func Handler() http.Handler {
	dist, _ := fs.Sub(files, "dist")
	assets := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			r.URL.Path = "/index.html"
		}
		assets.ServeHTTP(w, r)
	})
}
