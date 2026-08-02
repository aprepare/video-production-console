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
			page, err := fs.ReadFile(dist, "index.html")
			if err != nil {
				http.Error(w, "web console unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(page)
			return
		}
		assets.ServeHTTP(w, r)
	})
}
