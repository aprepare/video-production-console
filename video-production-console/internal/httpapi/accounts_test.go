package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

func TestCreateAccountStoresBackground(t *testing.T) {
	handler, db, dataRoot := newAccountsTestHandler(t)
	response := performAccountUpload(t, handler, "/api/accounts", "账号A", "background.png", pngBytes(t))
	defer response.Body.Close()

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.StatusCode, http.StatusCreated, readBody(t, response.Body))
	}
	var got struct {
		ID                string `json:"id"`
		Name              string `json:"name"`
		BackgroundAssetID string `json:"background_asset_id"`
		BackgroundPath    string `json:"background_path"`
	}
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "账号A" {
		t.Errorf("name = %q, want 账号A", got.Name)
	}
	if got.BackgroundAssetID == "" {
		t.Fatal("background_asset_id is empty")
	}
	var storedPath string
	if err := db.QueryRow(`SELECT path FROM assets WHERE id = ?`, got.BackgroundAssetID).Scan(&storedPath); err != nil {
		t.Fatalf("read background asset: %v", err)
	}
	absRoot, _ := filepath.Abs(dataRoot)
	absPath, _ := filepath.Abs(storedPath)
	if got.BackgroundPath != storedPath {
		t.Errorf("background_path = %q, want %q", got.BackgroundPath, storedPath)
	}
	if rel, err := filepath.Rel(absRoot, absPath); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		t.Fatalf("asset path %q is outside data root %q", absPath, absRoot)
	}
	if _, err := os.Stat(absPath); err != nil {
		t.Fatalf("stat stored background: %v", err)
	}
}

func TestCreateAccountValidation(t *testing.T) {
	handler, _, _ := newAccountsTestHandler(t)
	tests := []struct {
		name       string
		account    string
		filename   string
		data       []byte
		wantStatus int
		wantCode   string
	}{
		{name: "missing name", filename: "background.png", data: pngBytes(t), wantStatus: http.StatusBadRequest, wantCode: "account_name_required"},
		{name: "missing image", account: "账号A", wantStatus: http.StatusBadRequest, wantCode: "background_required"},
		{name: "fake PNG", account: "账号A", filename: "background.png", data: []byte("not an image"), wantStatus: http.StatusBadRequest, wantCode: "invalid_background"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := performAccountUpload(t, handler, "/api/accounts", tt.account, tt.filename, tt.data)
			defer response.Body.Close()
			assertAPIError(t, response, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestAccountMultipartEndpointsRejectOversizedRequest(t *testing.T) {
	handler, _, _ := newAccountsTestHandler(t)
	created := createAccount(t, handler, "账号A")
	systemTemp := t.TempDir()
	t.Setenv("TMP", systemTemp)
	t.Setenv("TEMP", systemTemp)
	for _, target := range []string{"/api/accounts", "/api/accounts/" + created.ID + "/background"} {
		t.Run(target, func(t *testing.T) {
			response := performAccountUpload(t, handler, target, "账号B", "huge.png", bytes.Repeat([]byte{'x'}, (21<<20)+1))
			defer response.Body.Close()
			assertAPIError(t, response, http.StatusRequestEntityTooLarge, "payload_too_large")
			entries, err := os.ReadDir(systemTemp)
			if err != nil {
				t.Fatalf("read system temp: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("oversized multipart left system temp entries: %v", entries)
			}
		})
	}
}

func TestCreateAccountRejectsDuplicateActiveName(t *testing.T) {
	handler, _, dataRoot := newAccountsTestHandler(t)
	first := performAccountUpload(t, handler, "/api/accounts", "账号A", "first.png", pngBytes(t))
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d, want %d", first.StatusCode, http.StatusCreated)
	}

	second := performAccountUpload(t, handler, "/api/accounts", "账号A", "second.png", pngBytes(t))
	defer second.Body.Close()
	assertAPIError(t, second, http.StatusConflict, "account_name_conflict")
	if files := backgroundFiles(t, dataRoot); len(files) != 1 {
		t.Fatalf("background files = %v, want only the successful upload", files)
	}
}

func TestCreateAccountRemovesNewFileWhenDatabaseWriteFails(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	handler := NewAccountsHandler(db, assets.NewService(root))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	response := performAccountUpload(t, handler, "/api/accounts", "账号A", "background.png", pngBytes(t))
	defer response.Body.Close()
	assertAPIError(t, response, http.StatusInternalServerError, "internal_error")
	if files := backgroundFiles(t, root); len(files) != 0 {
		t.Fatalf("background files remain after DB failure: %v", files)
	}
}

func TestListRenameReplaceBackgroundAndDeactivateAccount(t *testing.T) {
	handler, db, _ := newAccountsTestHandler(t)
	created := createAccount(t, handler, "账号A")

	listRequest := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body = %s", listRecorder.Code, http.StatusOK, listRecorder.Body.String())
	}
	var accounts []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(listRecorder.Body).Decode(&accounts); err != nil {
		t.Fatalf("decode account list: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != created.ID || accounts[0].Name != "账号A" {
		t.Fatalf("accounts = %+v, want the created account", accounts)
	}

	patchBody := strings.NewReader(`{"name":"账号B"}`)
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/accounts/"+created.ID, patchBody)
	patchRequest.Header.Set("Content-Type", "application/json")
	patchRecorder := httptest.NewRecorder()
	handler.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want %d; body = %s", patchRecorder.Code, http.StatusOK, patchRecorder.Body.String())
	}
	var renamed accountResponse
	if err := json.NewDecoder(patchRecorder.Body).Decode(&renamed); err != nil {
		t.Fatalf("decode renamed account: %v", err)
	}
	if renamed.Name != "账号B" {
		t.Errorf("renamed name = %q, want 账号B", renamed.Name)
	}

	oldPath := assetPath(t, db, created.BackgroundAssetID)
	replaced := performAccountUpload(t, handler, "/api/accounts/"+created.ID+"/background", "", "new.jpg", encodeJPEG(t))
	defer replaced.Body.Close()
	if replaced.StatusCode != http.StatusOK {
		t.Fatalf("replace background status = %d, want %d; body = %s", replaced.StatusCode, http.StatusOK, readBody(t, replaced.Body))
	}
	var updated accountResponse
	if err := json.NewDecoder(replaced.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated account: %v", err)
	}
	if updated.BackgroundAssetID == "" || updated.BackgroundAssetID == created.BackgroundAssetID {
		t.Errorf("new background ID = %q, want a new nonempty ID", updated.BackgroundAssetID)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("old background was not retained: %v", err)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/accounts/"+created.ID, nil)
	deleteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d; body = %s", deleteRecorder.Code, http.StatusNoContent, deleteRecorder.Body.String())
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM accounts WHERE id = ?`, created.ID).Scan(&status); err != nil {
		t.Fatalf("read deactivated account: %v", err)
	}
	if status != "inactive" {
		t.Errorf("status = %q, want inactive", status)
	}

	reused := performAccountUpload(t, handler, "/api/accounts", "账号B", "reuse.png", pngBytes(t))
	defer reused.Body.Close()
	if reused.StatusCode != http.StatusCreated {
		t.Fatalf("reused name status = %d, want %d; body = %s", reused.StatusCode, http.StatusCreated, readBody(t, reused.Body))
	}
}

func TestReplaceBackgroundKeepsCommittedFileWhenPostCommitAccountReadWouldFail(t *testing.T) {
	handler, db, _ := newAccountsTestHandler(t)
	created := createAccount(t, handler, "账号A")
	_, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER corrupt_account_timestamp_after_replacement
        AFTER INSERT ON assets WHEN NEW.account_id = '%s' AND NEW.version = 2
        BEGIN
            UPDATE accounts SET created_at = 'not-a-timestamp' WHERE id = NEW.account_id;
        END`, created.ID))
	if err != nil {
		t.Fatalf("create fault trigger: %v", err)
	}

	response := performAccountUpload(t, handler, "/api/accounts/"+created.ID+"/background", "", "new.jpg", encodeJPEG(t))
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("replace status = %d, want %d; body = %s", response.StatusCode, http.StatusOK, readBody(t, response.Body))
	}
	var assetID, path string
	if err := db.QueryRow(`SELECT background_asset_id FROM accounts WHERE id = ?`, created.ID).Scan(&assetID); err != nil {
		t.Fatalf("read committed pointer: %v", err)
	}
	if err := db.QueryRow(`SELECT path FROM assets WHERE id = ?`, assetID).Scan(&path); err != nil {
		t.Fatalf("read committed asset: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("committed background file was deleted: %v", err)
	}
}

func TestReplaceBackgroundDoesNotDeleteFileWhenCommitOutcomeIsUnknown(t *testing.T) {
	root := t.TempDir()
	repository := &unknownCommitRepository{account: domain.Account{ID: "4d739048-2e42-45d3-8128-4dd9b1cae664", Name: "账号A"}}
	handler := newAccountsHandler(repository, assets.NewService(root))
	response := performAccountUpload(t, handler, "/api/accounts/"+repository.account.ID+"/background", "", "new.jpg", encodeJPEG(t))
	defer response.Body.Close()
	assertAPIError(t, response, http.StatusInternalServerError, "internal_error")
	if repository.savedPath == "" {
		t.Fatal("repository did not receive saved background")
	}
	if _, err := os.Stat(repository.savedPath); err != nil {
		t.Fatalf("file was deleted for unknown commit outcome: %v", err)
	}
}

type unknownCommitRepository struct {
	account   domain.Account
	savedPath string
}

func (r *unknownCommitRepository) List(context.Context) ([]domain.Account, error) { return nil, nil }
func (r *unknownCommitRepository) CreateWithBackground(context.Context, domain.Account, store.NewBackground) (store.CommitState, error) {
	return store.CommitCommitted, nil
}
func (r *unknownCommitRepository) Get(context.Context, string) (domain.Account, error) {
	return r.account, nil
}
func (r *unknownCommitRepository) Rename(context.Context, string, string, time.Time) (domain.Account, error) {
	return r.account, nil
}
func (r *unknownCommitRepository) ReplaceBackground(_ context.Context, _ string, background store.NewBackground, _ time.Time) (domain.Account, store.CommitState, error) {
	r.savedPath = background.Path
	return domain.Account{}, store.CommitUnknown, errors.New("simulated unknown commit result")
}
func (r *unknownCommitRepository) Deactivate(context.Context, string, time.Time) error { return nil }

func TestAccountMutationErrorsAreStable(t *testing.T) {
	handler, _, _ := newAccountsTestHandler(t)
	missingID := "4d739048-2e42-45d3-8128-4dd9b1cae664"
	tests := []struct {
		name       string
		method     string
		target     string
		body       io.Reader
		content    string
		wantStatus int
		wantCode   string
	}{
		{name: "invalid ID", method: http.MethodPatch, target: "/api/accounts/not-a-uuid", body: strings.NewReader(`{"name":"B"}`), content: "application/json", wantStatus: http.StatusBadRequest, wantCode: "invalid_account_id"},
		{name: "missing rename", method: http.MethodPatch, target: "/api/accounts/" + missingID, body: strings.NewReader(`{"name":"B"}`), content: "application/json", wantStatus: http.StatusNotFound, wantCode: "account_not_found"},
		{name: "missing delete", method: http.MethodDelete, target: "/api/accounts/" + missingID, wantStatus: http.StatusNotFound, wantCode: "account_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.target, tt.body)
			request.Header.Set("Content-Type", tt.content)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			assertAPIError(t, recorder.Result(), tt.wantStatus, tt.wantCode)
		})
	}
}

func newAccountsTestHandler(t *testing.T) (http.Handler, *sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewAccountsHandler(db, assets.NewService(root)), db, root
}

func performAccountUpload(t *testing.T, handler http.Handler, target, name, filename string, data []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if name != "" {
		if err := writer.WriteField("name", name); err != nil {
			t.Fatalf("write name: %v", err)
		}
	}
	if filename != "" {
		part, err := writer.CreateFormFile("background", filename)
		if err != nil {
			t.Fatalf("create background field: %v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("write background: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Result()
}

func createAccount(t *testing.T, handler http.Handler, name string) accountResponse {
	t.Helper()
	response := performAccountUpload(t, handler, "/api/accounts", name, "background.png", pngBytes(t))
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body = %s", response.StatusCode, http.StatusCreated, readBody(t, response.Body))
	}
	var account accountResponse
	if err := json.NewDecoder(response.Body).Decode(&account); err != nil {
		t.Fatalf("decode created account: %v", err)
	}
	return account
}

func assetPath(t *testing.T, db *sql.DB, assetID string) string {
	t.Helper()
	var path string
	if err := db.QueryRow(`SELECT path FROM assets WHERE id = ?`, assetID).Scan(&path); err != nil {
		t.Fatalf("read asset path: %v", err)
	}
	return path
}

func encodeJPEG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0x80, G: 0x40, B: 0x20, A: 0xff})
	if err := jpeg.Encode(&buffer, img, nil); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return buffer.Bytes()
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0x40, G: 0x80, B: 0xc0, A: 0xff})
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buffer.Bytes()
}

func assertAPIError(t *testing.T, response *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	if response.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d; body = %s", response.StatusCode, wantStatus, readBody(t, response.Body))
	}
	var got struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode API error: %v", err)
	}
	if got.Code != wantCode {
		t.Errorf("code = %q, want %q", got.Code, wantCode)
	}
	if strings.TrimSpace(got.Message) == "" {
		t.Error("message is empty")
	}
}

func readBody(t *testing.T, reader io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

func backgroundFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	accountsRoot := filepath.Join(root, "accounts")
	_ = filepath.WalkDir(accountsRoot, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatalf("walk backgrounds: %v", err)
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files
}
