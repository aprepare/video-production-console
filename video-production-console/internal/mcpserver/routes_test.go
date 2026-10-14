package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// registeredRoutes 静态扫描控制台注册的全部 /api 路由（HandleFunc 与 map 注册两种写法）。
func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("../httpapi/*.go")
	if err != nil {
		t.Fatal(err)
	}
	appFiles, _ := filepath.Glob("../app/*.go")
	files = append(files, appFiles...)
	pattern := regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) (/api/[^"\s]+)"`)
	routes := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range pattern.FindAllStringSubmatch(string(raw), -1) {
			routes[m[1]+" "+normalizeRoute(m[2])] = true
		}
	}
	// 手写分发、不走 HandleFunc 的路由。
	for _, route := range []string{"GET /api/health", "GET /api/runtime", "GET /api/dependencies", "POST /api/dependencies/baokuan-mcp/configure",
		"GET /api/library/materials/search", "POST /api/library/materials/bundle"} {
		routes[route] = true
	}
	return routes
}

var anyParam = regexp.MustCompile(`\{[a-zA-Z_.]+\}`)

func normalizeRoute(path string) string { return anyParam.ReplaceAllString(path, "{}") }

func TestToolsHitRegisteredRoutes(t *testing.T) {
	routes := registeredRoutes(t)
	if len(routes) < 100 {
		t.Fatalf("route scan found only %d routes; scanner is broken", len(routes))
	}
	for _, tool := range Catalog() {
		if tool.Run != nil {
			continue
		}
		key := tool.Method + " " + normalizeRoute(tool.Path)
		if !routes[key] {
			t.Errorf("tool %s → %s is not a registered route", tool.Name, key)
		}
	}
}

func TestAPICatalogListsRegisteredRoutes(t *testing.T) {
	routes := registeredRoutes(t)
	line := regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE) (/api/\S+)`)
	listed := map[string]bool{}
	for _, entry := range strings.Split(APICatalog, "\n") {
		m := line.FindStringSubmatch(entry)
		if m == nil {
			continue
		}
		key := m[1] + " " + normalizeRoute(m[2])
		listed[key] = true
		if !routes[key] {
			t.Errorf("catalog lists %s but no such route", key)
		}
	}
	for route := range routes {
		if strings.HasPrefix(route, "GET /api/auth") || strings.HasPrefix(route, "POST /api/auth") || strings.Contains(route, "/api/mcp/") ||
			strings.Contains(route, "/api/workspace") || strings.Contains(route, "/api/ai-shorts") {
			continue
		}
		if !listed[route] {
			t.Errorf("route %s is missing from APICatalog", route)
		}
	}
}
