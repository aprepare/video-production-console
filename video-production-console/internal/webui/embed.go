package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/* dist/assets/*
var files embed.FS

func Handler() http.Handler {
	dist, err := fs.Sub(files, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "web console unavailable", http.StatusInternalServerError)
		})
	}
	assets := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index.html" {
			serveIndex(w, dist)
			return
		}
		if _, err := fs.Stat(dist, path); err == nil {
			assets.ServeHTTP(w, r)
			return
		}
		// React client routes such as /projects/{id} must fall back to the
		// shell page so a browser refresh can rehydrate the workbench.
		serveIndex(w, dist)
	})
}

func serveIndex(w http.ResponseWriter, dist fs.FS) {
	page, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "web console unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}
