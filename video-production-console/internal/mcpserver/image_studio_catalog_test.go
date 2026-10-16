package mcpserver

import (
	"net/http"
	"strings"
	"testing"
)

func TestImageStudioRoutesAreDocumentedWithSafetyMarkers(t *testing.T) {
	want := []string{
		"GET /api/image-studio/models |",
		"POST /api/image-studio/enhance | 同步扩写生图提示词 | $",
		"POST /api/image-studio/generations | 创建生图任务 | $",
		"GET /api/image-studio/generations |",
		"GET /api/image-studio/generations/{id} |",
		"PATCH /api/image-studio/generations/{id} |",
		"DELETE /api/image-studio/generations/{id} | 删除生图记录及图片 | !",
		"POST /api/image-studio/generations/{id}/retry | 重试失败图片 | $",
		"GET /api/image-studio/outputs/{id}/image |",
		"GET /api/image-studio/generations/{id}/download |",
	}
	for _, entry := range want {
		if !strings.Contains(APICatalog, entry) {
			t.Errorf("APICatalog missing %q", entry)
		}
	}
}

func TestImageStudioToolsExposeAllRequestFields(t *testing.T) {
	tools := map[string]Tool{}
	for _, tool := range Catalog() {
		tools[tool.Name] = tool
	}

	generate, ok := tools["generate_image"]
	if !ok {
		t.Fatal("generate_image tool missing")
	}
	if generate.Method != http.MethodPost || generate.Path != "/api/image-studio/generations" {
		t.Fatalf("generate_image route = %s %s", generate.Method, generate.Path)
	}
	if generate.Confirm != "会调用生图服务（花钱）" {
		t.Fatalf("generate_image confirm = %q", generate.Confirm)
	}
	wantGenerate := []string{"prompt", "final_prompt", "enhance", "style", "custom_style", "model", "ratio", "count", "text_model"}
	assertParamSet(t, generate.Params, wantGenerate)
	for _, name := range []string{"prompt", "ratio", "count"} {
		if !generate.Params[name].Required {
			t.Errorf("generate_image %s must be required by the HTTP contract", name)
		}
	}

	list, ok := tools["list_image_generations"]
	if !ok {
		t.Fatal("list_image_generations tool missing")
	}
	if list.Method != http.MethodGet || list.Path != "/api/image-studio/generations" {
		t.Fatalf("list_image_generations route = %s %s", list.Method, list.Path)
	}
	wantList := []string{"limit", "before", "favorite", "q"}
	assertParamSet(t, list.Params, wantList)
	for _, name := range wantList {
		if list.Params[name].In != "query" {
			t.Errorf("list_image_generations %s must be a query parameter", name)
		}
	}
}

func assertParamSet(t *testing.T, got map[string]Param, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("params count = %d, want %d: %#v", len(got), len(want), got)
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("missing parameter %q", name)
		}
	}
}
