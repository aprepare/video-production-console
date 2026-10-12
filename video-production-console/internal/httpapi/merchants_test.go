package httpapi

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/store"
)

func merchantAPI(t *testing.T) (http.Handler, *sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewMerchantsHandler(db, root), db, root
}

func merchantRequest(t *testing.T, h http.Handler, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: status %d want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	return w
}

func merchantJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMerchantsHTTPCRUDAndValidation(t *testing.T) {
	h, _, _ := merchantAPI(t)
	merchantRequest(t, h, "GET", "/api/merchants", "", 200)
	for _, body := range []string{`{"name":"茶店","status":"invalid"}`, `{"name":""}`, `{"name":"茶店","unknown":true}`} {
		merchantRequest(t, h, "POST", "/api/merchants", body, 400)
	}
	m := merchantJSON(t, merchantRequest(t, h, "POST", "/api/merchants", `{"name":"茶店","contact":"张三","status":"trial"}`, 201))
	id := m["id"].(string)
	merchantRequest(t, h, "GET", "/api/merchants/bad-id", "", 400)
	merchantRequest(t, h, "GET", "/api/merchants/"+uuid.NewString(), "", 404)
	merchantRequest(t, h, "PATCH", "/api/merchants/"+id, `{"status":"wrong"}`, 400)
	got := merchantJSON(t, merchantRequest(t, h, "PATCH", "/api/merchants/"+id, `{"phone":"123","status":"active"}`, 200))
	if got["name"] != "茶店" || got["phone"] != "123" {
		t.Fatal(got)
	}
	list := merchantRequest(t, h, "GET", "/api/merchants?status=active&q=张三", "", 200)
	if !strings.Contains(list.Body.String(), id) {
		t.Fatal(list.Body.String())
	}
	if w := merchantRequest(t, h, "GET", "/api/merchants?status=paused", "", 200); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
	merchantRequest(t, h, "GET", "/api/merchants?status=bad", "", 400)
	merchantRequest(t, h, "DELETE", "/api/merchants/"+id, "", 204)
	merchantRequest(t, h, "GET", "/api/merchants/"+id, "", 404)
}

func TestMerchantOrdersHTTPAccountingAndProjects(t *testing.T) {
	h, db, _ := merchantAPI(t)
	m := merchantJSON(t, merchantRequest(t, h, "POST", "/api/merchants", `{"name":"海鲜店"}`, 201))
	mid := m["id"].(string)
	create := func(title string) string {
		o := merchantJSON(t, merchantRequest(t, h, "POST", "/api/orders", fmt.Sprintf(`{"merchant_id":%q,"title":%q,"price_cents":15000,"paid_cents":9000}`, mid, title), 201))
		return o["id"].(string)
	}
	oid, other := create("月度"), create("另单")
	for _, body := range []string{`{"status":"bad"}`, `{"package":"bad"}`, `{"paid_cents":-1}`} {
		merchantRequest(t, h, "PATCH", "/api/orders/"+oid, body, 400)
	}
	merchantRequest(t, h, "POST", "/api/orders/"+oid+"/costs", `{"kind":"bad"}`, 400)
	merchantRequest(t, h, "POST", "/api/orders/"+oid+"/costs", `{"kind":""}`, 400)
	merchantRequest(t, h, "GET", "/api/orders?merchant_id=bad", "", 400)
	merchantRequest(t, h, "GET", "/api/orders?status=bad", "", 400)
	merchantRequest(t, h, "PATCH", "/api/orders/"+oid, `{"status":"producing","paid_cents":10000}`, 200)
	c := merchantJSON(t, merchantRequest(t, h, "POST", "/api/orders/"+oid+"/costs", `{"kind":"labor","minutes":60,"amount_cents":2000}`, 201))
	merchantRequest(t, h, "POST", "/api/orders/"+oid+"/costs", `{"kind":"revision","minutes":10,"amount_cents":500}`, 201)
	aid, pid := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,'a','#fff','active',?,?)`, aid, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','script',?,?)`, pid, aid, now, now); err != nil {
		t.Fatal(err)
	}
	merchantRequest(t, h, "POST", "/api/orders/"+oid+"/projects", fmt.Sprintf(`{"project_id":%q}`, pid), 204)
	merchantRequest(t, h, "POST", "/api/orders/"+other+"/projects/"+pid+"/deliver", "", 404)
	merchantRequest(t, h, "DELETE", "/api/orders/"+other+"/projects/"+pid, "", 404)
	merchantRequest(t, h, "POST", "/api/orders/"+oid+"/projects/"+pid+"/deliver", "", 204)
	detail := merchantJSON(t, merchantRequest(t, h, "GET", "/api/orders/"+oid, "", 200))
	summary := detail["summary"].(map[string]any)
	if summary["cost_cents"] != float64(2500) || summary["contribution_cents"] != float64(7500) || summary["labor_minutes"] != float64(60) || summary["revision_count"] != float64(1) || summary["delivered_project_count"] != float64(1) {
		t.Fatal(summary)
	}
	overview := merchantJSON(t, merchantRequest(t, h, "GET", "/api/business/overview", "", 200))
	if overview["price_cents"] != float64(30000) || overview["paid_cents"] != float64(19000) || overview["contribution_cents"] != float64(16500) {
		t.Fatal(overview)
	}
	merchantRequest(t, h, "DELETE", "/api/order-costs/"+c["id"].(string), "", 204)
	merchantRequest(t, h, "DELETE", "/api/orders/"+oid+"/projects/"+pid, "", 204)
	detail = merchantJSON(t, merchantRequest(t, h, "GET", "/api/orders/"+oid, "", 200))
	if len(detail["projects"].([]any)) != 0 {
		t.Fatal(detail)
	}
	merchantRequest(t, h, "DELETE", "/api/orders/"+other, "", 204)
	merchantRequest(t, h, "DELETE", "/api/merchants/"+mid, "", 204)
	merchantRequest(t, h, "GET", "/api/orders/"+oid, "", 404)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM order_costs`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("costs=%d err=%v", n, err)
	}
}

func TestMerchantMaterialUploadFailureCleansFiles(t *testing.T) {
	h, db, root := merchantAPI(t)
	m := merchantJSON(t, merchantRequest(t, h, "POST", "/api/merchants", `{"name":"店铺"}`, 201))
	mid := m["id"].(string)
	merchantRequest(t, h, "POST", "/api/merchants/"+mid+"/materials", `{}`, 400)
	uploadMerchantMaterial(t, h, uuid.NewString(), "clip.mp4", nil, 404)
	if _, err := db.Exec(`CREATE TRIGGER reject_material BEFORE INSERT ON merchant_materials BEGIN SELECT RAISE(ABORT, 'test storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	uploadMerchantMaterial(t, h, mid, "clip.mp4", nil, 500)
	files, err := filepath.Glob(filepath.Join(root, "merchants", mid, "materials", "*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("failed upload left files=%v err=%v", files, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM merchant_materials`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("materials=%d err=%v", n, err)
	}
}

func TestMerchantMaterialKindsAndMerchantFileCascade(t *testing.T) {
	h, _, _ := merchantAPI(t)
	m := merchantJSON(t, merchantRequest(t, h, "POST", "/api/merchants", `{"name":"店铺"}`, 201))
	mid := m["id"].(string)
	paths := []string{}
	for _, tc := range []struct{ filename, kind string }{{"../clip.MOV", "video"}, {"photo.PNG", "image"}, {"sound.m4a", "audio"}, {"license.pdf", "document"}, {"raw.bin", "other"}} {
		mat := merchantJSON(t, uploadMerchantMaterial(t, h, mid, tc.filename, nil, 201))
		if mat["kind"] != tc.kind {
			t.Fatal(mat)
		}
		paths = append(paths, mat["path"].(string))
	}
	merchantRequest(t, h, "DELETE", "/api/merchants/"+mid, "", 204)
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cascade file %s: %v", path, err)
		}
	}
}

func TestMerchantMaterialRejectsSymlinkEscape(t *testing.T) {
	h, db, root := merchantAPI(t)
	m := merchantJSON(t, merchantRequest(t, h, "POST", "/api/merchants", `{"name":"店铺"}`, 201))
	mid := m["id"].(string)
	mat := merchantJSON(t, uploadMerchantMaterial(t, h, mid, "clip.mp4", nil, 201))
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "merchants", "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := db.Exec(`UPDATE merchant_materials SET path=? WHERE id=?`, filepath.Join(link, "keep.txt"), mat["id"]); err != nil {
		t.Fatal(err)
	}
	merchantRequest(t, h, "GET", "/api/materials/"+mat["id"].(string)+"/content", "", 400)
	merchantRequest(t, h, "DELETE", "/api/materials/"+mat["id"].(string), "", 400)
	if data, err := os.ReadFile(filepath.Join(outside, "keep.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("outside data=%q err=%v", data, err)
	}
}

func uploadMerchantMaterial(t *testing.T, h http.Handler, mid, filename string, fields map[string]string, want int) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "hello material"); err != nil {
		t.Fatal(err)
	}
	for k, v := range fields {
		if err := form.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/merchants/"+mid+"/materials", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("upload: %d want %d: %s", w.Code, want, w.Body.String())
	}
	return w
}

func TestMerchantMaterialUploadContentDeleteAndPathProtection(t *testing.T) {
	h, db, root := merchantAPI(t)
	m := merchantJSON(t, merchantRequest(t, h, "POST", "/api/merchants", `{"name":"餐厅"}`, 201))
	mid := m["id"].(string)
	uploadMerchantMaterial(t, h, mid, "clip.MP4", map[string]string{"kind": "bad"}, 400)
	uploadMerchantMaterial(t, h, mid, "clip.MP4", map[string]string{"license_source": "bad"}, 400)
	mat := merchantJSON(t, uploadMerchantMaterial(t, h, mid, "clip.MP4", map[string]string{"description": "店内", "license_scope": "代运营"}, 201))
	id, path := mat["id"].(string), mat["path"].(string)
	sum := sha256.Sum256([]byte("hello material"))
	if mat["sha256"] != fmt.Sprintf("%x", sum) || mat["size"] != float64(14) || mat["kind"] != "video" {
		t.Fatal(mat)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "hello material" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	w := merchantRequest(t, h, "GET", "/api/materials/"+id+"/content", "", 200)
	if w.Body.String() != "hello material" {
		t.Fatal(w.Body.String())
	}
	merchantRequest(t, h, "PATCH", "/api/materials/"+id, `{"kind":"bad"}`, 400)
	merchantRequest(t, h, "PATCH", "/api/materials/"+id, `{"license_source":"bad"}`, 400)
	merchantRequest(t, h, "PATCH", "/api/materials/"+id, `{"description":"新描述","license_proof":"授权书"}`, 200)
	merchantRequest(t, h, "PATCH", "/api/materials/"+id, `{"path":"outside"}`, 400)
	merchantRequest(t, h, "GET", "/api/merchants/"+mid+"/materials", "", 200)
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{outside, filepath.Join(root, "merchants", "..", "outside.txt"), filepath.Join(root, "merchants-other", "file.txt"), filepath.Join(root, "merchants")} {
		if _, err := db.Exec(`UPDATE merchant_materials SET path=? WHERE id=?`, unsafe, id); err != nil {
			t.Fatal(err)
		}
		merchantRequest(t, h, "DELETE", "/api/materials/"+id, "", 400)
		merchantRequest(t, h, "GET", "/api/materials/"+id+"/content", "", 400)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE merchant_materials SET path=? WHERE id=?`, path, id); err != nil {
		t.Fatal(err)
	}
	merchantRequest(t, h, "DELETE", "/api/materials/"+id, "", 204)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("deleted file err=%v", err)
	}
	if w := merchantRequest(t, h, "GET", "/api/merchants/"+mid+"/materials", "", 200); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
}
