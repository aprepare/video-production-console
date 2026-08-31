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
			// 构建产物文件名带内容哈希，可放心长缓存；换版本时
			// index.html 引用的哈希会变，旧文件自然失效。
			if strings.HasPrefix(path, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
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
	// index.html 不带哈希：禁止浏览器启发式缓存，否则换版本后
	// 用户会一直看到旧页面（嵌入文件没有修改时间，浏览器只能瞎猜）。
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}
