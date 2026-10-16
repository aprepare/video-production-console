package mcpserver

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestVideoQCAPICatalogEntries(t *testing.T) {
	wants := []string{
		"POST /api/video-qc ",
		"GET /api/video-qc ",
		"GET /api/video-qc/{id} ",
		"GET /api/video-qc/{id}/frames/{n} ",
		"GET /api/video-qc/{id}/video ",
		"DELETE /api/video-qc/{id} ",
	}
	for _, want := range wants {
		if count := strings.Count(APICatalog, want); count != 1 {
			t.Errorf("APICatalog entry %q count = %d, want 1", want, count)
		}
	}
	if !strings.Contains(APICatalog, "POST /api/video-qc |") || !strings.Contains(APICatalog, "POST /api/video-qc | 创建视频审核") ||
		!strings.Contains(APICatalog, "POST /api/video-qc | 创建视频审核 {source, source_id, product, model}（视觉模型，异步；上传来源使用 multipart） | $") {
		t.Error("POST /api/video-qc must be documented as a paid model call")
	}
	if !strings.Contains(APICatalog, "DELETE /api/video-qc/{id} | 删除审核记录及文件 | !") {
		t.Error("DELETE /api/video-qc/{id} must be documented as destructive")
	}
}

func TestCheckVideoQualityToolSchemaAndRequest(t *testing.T) {
	var got Tool
	for _, tool := range Catalog() {
		if tool.Name == "check_video_quality" {
			got = tool
			break
		}
	}
	if got.Name == "" {
		t.Fatal("check_video_quality tool is missing")
	}
	if got.Method != http.MethodPost || got.Path != "/api/video-qc" {
		t.Fatalf("route = %s %s, want POST /api/video-qc", got.Method, got.Path)
	}
	if got.Confirm != "会调用看图模型（花钱）" {
		t.Fatalf("Confirm = %q", got.Confirm)
	}
	if got.Params["source"].Required != true || !reflect.DeepEqual(got.Params["source"].Enum, []string{"reference", "image_video_job"}) {
		t.Fatalf("source schema = %#v", got.Params["source"])
	}
	if !got.Params["source_id"].Required {
		t.Fatal("source_id must be required")
	}
	method, path, query, body, err := got.request(map[string]any{
		"source": "reference", "source_id": "ref-1", "product": "蓝色包装", "model": "vision-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantBody := map[string]any{"source": "reference", "source_id": "ref-1", "product": "蓝色包装", "model": "vision-1"}
	if method != http.MethodPost || path != "/api/video-qc" || len(query) != 0 || !reflect.DeepEqual(body, wantBody) {
		t.Fatalf("request = %s %s %v %#v", method, path, query, body)
	}
	descriptor := got.descriptor()
	if !strings.Contains(descriptor["description"].(string), "需要用户确认：会调用看图模型（花钱）") {
		t.Fatalf("descriptor does not carry the confirmation warning: %q", descriptor["description"])
	}
}

func TestAnalyzeReferenceToolAcceptsProduct(t *testing.T) {
	for _, tool := range Catalog() {
		if tool.Name != "analyze_reference" {
			continue
		}
		if _, ok := tool.Params["product"]; !ok {
			t.Fatal("analyze_reference schema is missing product")
		}
		_, _, _, body, err := tool.request(map[string]any{"reference_id": "ref-1", "product": "用户自己的海鲜礼盒"})
		if err != nil {
			t.Fatal(err)
		}
		if body.(map[string]any)["product"] != "用户自己的海鲜礼盒" {
			t.Fatalf("product mapping = %#v", body)
		}
		return
	}
	t.Fatal("analyze_reference tool is missing")
}
