package mcpserver

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestReplicateAPICatalogEntries(t *testing.T) {
	wants := []string{
		"GET /api/replicate/providers ",
		"POST /api/replicate ",
		"GET /api/replicate ",
		"GET /api/replicate/{id} ",
		"PATCH /api/replicate/{id} ",
		"DELETE /api/replicate/{id} ",
		"POST /api/replicate/{id}/shots ",
		"PATCH /api/replicate/shots/{shot_id} ",
		"DELETE /api/replicate/shots/{shot_id} ",
		"POST /api/replicate/{id}/shots/reorder ",
		"POST /api/replicate/{id}/first-frames ",
		"POST /api/replicate/shots/{shot_id}/first-frame ",
		"POST /api/replicate/shots/{shot_id}/first-frame/reference ",
		"GET /api/replicate/shots/{shot_id}/first-frame ",
		"POST /api/replicate/{id}/generate ",
		"POST /api/replicate/takes/{take_id}/select ",
		"POST /api/replicate/takes/{take_id}/cancel ",
		"DELETE /api/replicate/takes/{take_id} ",
		"GET /api/replicate/takes/{take_id}/video ",
		"POST /api/replicate/{id}/stitch ",
		"GET /api/replicate/{id}/final ",
		"POST /api/replicate/{id}/qc ",
	}
	for _, want := range wants {
		if count := strings.Count(APICatalog, want); count != 1 {
			t.Errorf("APICatalog entry %q count = %d, want 1", want, count)
		}
	}
	for _, paid := range []string{
		"POST /api/replicate/{id}/first-frames |", "POST /api/replicate/{id}/generate |",
		"POST /api/replicate/{id}/stitch |", "POST /api/replicate/{id}/qc |",
	} {
		line := catalogLine(APICatalog, paid)
		if !strings.HasSuffix(line, "| $") {
			t.Errorf("paid route %q is missing $ marker: %q", paid, line)
		}
	}
	for _, destructive := range []string{
		"DELETE /api/replicate/{id} |", "DELETE /api/replicate/shots/{shot_id} |", "DELETE /api/replicate/takes/{take_id} |",
	} {
		line := catalogLine(APICatalog, destructive)
		if !strings.HasSuffix(line, "| !") {
			t.Errorf("destructive route %q is missing ! marker: %q", destructive, line)
		}
	}
}

func catalogLine(catalog, prefix string) string {
	for _, line := range strings.Split(catalog, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

func TestReplicateToolsSchemasAndRequests(t *testing.T) {
	wants := map[string]struct {
		method  string
		path    string
		confirm string
		args    map[string]any
		body    map[string]any
	}{
		"create_replicate_project": {
			method: http.MethodPost, path: "/api/replicate",
			args: map[string]any{"reference_id": "ref-1", "analysis_id": "ana-1", "product": "海鲜礼盒", "ratio": "9:16", "video_provider": "grok", "image_model": "img-1", "variants": 2},
			body: map[string]any{"reference_id": "ref-1", "analysis_id": "ana-1", "product": "海鲜礼盒", "ratio": "9:16", "video_provider": "grok", "image_model": "img-1", "variants": 2},
		},
		"generate_replicate_shots": {
			method: http.MethodPost, path: "/api/replicate/{id}/generate", confirm: "会调用视频模型（花钱）",
			args: map[string]any{"id": "project-1", "shot_ids": []any{"shot-1"}, "variants": 3, "provider": "runninghub_h3"},
			body: map[string]any{"shot_ids": []any{"shot-1"}, "variants": 3, "provider": "runninghub_h3"},
		},
		"stitch_replicate": {
			method: http.MethodPost, path: "/api/replicate/{id}/stitch",
			args: map[string]any{"id": "project-1", "audio": "none"}, body: map[string]any{"audio": "none"},
		},
		"get_replicate_project": {
			method: http.MethodGet, path: "/api/replicate/{id}", args: map[string]any{"id": "project-1"}, body: nil,
		},
	}
	byName := map[string]Tool{}
	for _, tool := range Catalog() {
		byName[tool.Name] = tool
	}
	for name, want := range wants {
		t.Run(name, func(t *testing.T) {
			tool, ok := byName[name]
			if !ok {
				t.Fatalf("tool %s is missing", name)
			}
			if tool.Method != want.method || tool.Path != want.path || tool.Confirm != want.confirm {
				t.Fatalf("tool = method=%q path=%q confirm=%q", tool.Method, tool.Path, tool.Confirm)
			}
			method, path, query, body, err := tool.request(want.args)
			if err != nil {
				t.Fatal(err)
			}
			bodyMatches := reflect.DeepEqual(body, want.body)
			if want.body == nil && (body == nil || (reflect.ValueOf(body).Kind() == reflect.Map && reflect.ValueOf(body).IsNil())) {
				bodyMatches = true
			}
			if method != want.method || path != strings.ReplaceAll(want.path, "{id}", "project-1") || len(query) != 0 || !bodyMatches {
				t.Fatalf("request = %s %s %v %#v", method, path, query, body)
			}
		})
	}
}
