package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type accountsHandler struct {
	repository *store.AccountRepository
	assets     *assets.Service
}

type accountResponse struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	BackgroundAssetID string `json:"background_asset_id"`
	BackgroundPath    string `json:"background_path"`
	Color             string `json:"color"`
	Status            string `json:"status"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

func NewAccountsHandler(db *sql.DB, assetService *assets.Service) http.Handler {
	handler := &accountsHandler{repository: store.NewAccountRepository(db), assets: assetService}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/accounts", handler.list)
	mux.HandleFunc("POST /api/accounts", handler.create)
	mux.HandleFunc("PATCH /api/accounts/{id}", handler.rename)
	mux.HandleFunc("POST /api/accounts/{id}/background", handler.replaceBackground)
	mux.HandleFunc("DELETE /api/accounts/{id}", handler.deactivate)
	return mux
}

func (h *accountsHandler) list(response http.ResponseWriter, request *http.Request) {
	accounts, err := h.repository.List(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "accounts_list_failed", "Accounts could not be listed.")
		return
	}
	result := make([]accountResponse, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, toAccountResponse(account))
	}
	writeJSON(response, http.StatusOK, result)
}

func (h *accountsHandler) create(response http.ResponseWriter, request *http.Request) {
	if err := request.ParseMultipartForm(21 << 20); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_multipart", "The multipart form could not be read.")
		return
	}
	defer request.MultipartForm.RemoveAll()
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" {
		writeError(response, http.StatusBadRequest, "account_name_required", "Account name is required.")
		return
	}
	file, header, err := request.FormFile("background")
	if err != nil {
		writeError(response, http.StatusBadRequest, "background_required", "A background image is required.")
		return
	}
	defer file.Close()

	accountID := uuid.NewString()
	saved, err := h.assets.SaveAccountBackground(accountID, header.Filename, file)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_background", "Background must be a PNG, JPEG, or WebP image up to 20 MiB.")
		return
	}
	assetID := uuid.NewString()
	now := time.Now().UTC()
	account := domain.Account{
		ID: accountID, Name: name, BackgroundAssetID: &assetID,
		BackgroundPath: &saved.Path,
		Color:          "#5B8FF9", Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	err = h.repository.CreateWithBackground(request.Context(), account, store.NewBackground{
		ID: assetID, Path: saved.Path, Filename: safeFilename(header.Filename), MIMEType: saved.MIMEType,
		Size: saved.Size, SHA256: saved.SHA256,
	})
	if err != nil {
		_ = os.Remove(saved.Path)
		if errors.Is(err, store.ErrAccountNameConflict) {
			writeError(response, http.StatusConflict, "account_name_conflict", "An active account with this name already exists.")
			return
		}
		writeError(response, http.StatusInternalServerError, "account_create_failed", "The account could not be created.")
		return
	}
	writeJSON(response, http.StatusCreated, toAccountResponse(account))
}

func (h *accountsHandler) rename(response http.ResponseWriter, request *http.Request) {
	id, ok := accountID(response, request.PathValue("id"))
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "A JSON object with a name is required.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		writeError(response, http.StatusBadRequest, "account_name_required", "Account name is required.")
		return
	}
	account, err := h.repository.Rename(request.Context(), id, input.Name, time.Now().UTC())
	if errors.Is(err, store.ErrAccountNotFound) {
		writeError(response, http.StatusNotFound, "account_not_found", "The account was not found.")
		return
	}
	if errors.Is(err, store.ErrAccountNameConflict) {
		writeError(response, http.StatusConflict, "account_name_conflict", "An active account with this name already exists.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "account_update_failed", "The account could not be updated.")
		return
	}
	writeJSON(response, http.StatusOK, toAccountResponse(account))
}

func (h *accountsHandler) replaceBackground(response http.ResponseWriter, request *http.Request) {
	id, ok := accountID(response, request.PathValue("id"))
	if !ok {
		return
	}
	if _, err := h.repository.Get(request.Context(), id); errors.Is(err, store.ErrAccountNotFound) {
		writeError(response, http.StatusNotFound, "account_not_found", "The account was not found.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "account_read_failed", "The account could not be read.")
		return
	}
	if err := request.ParseMultipartForm(21 << 20); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_multipart", "The multipart form could not be read.")
		return
	}
	defer request.MultipartForm.RemoveAll()
	file, header, err := request.FormFile("background")
	if err != nil {
		writeError(response, http.StatusBadRequest, "background_required", "A background image is required.")
		return
	}
	defer file.Close()
	saved, err := h.assets.SaveAccountBackground(id, header.Filename, file)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_background", "Background must be a PNG, JPEG, or WebP image up to 20 MiB.")
		return
	}
	background := store.NewBackground{
		ID: uuid.NewString(), Path: saved.Path, Filename: safeFilename(header.Filename), MIMEType: saved.MIMEType,
		Size: saved.Size, SHA256: saved.SHA256,
	}
	account, err := h.repository.ReplaceBackground(request.Context(), id, background, time.Now().UTC())
	if err != nil {
		_ = os.Remove(saved.Path)
		if errors.Is(err, store.ErrAccountNotFound) {
			writeError(response, http.StatusNotFound, "account_not_found", "The account was not found.")
			return
		}
		writeError(response, http.StatusInternalServerError, "background_update_failed", "The background could not be updated.")
		return
	}
	writeJSON(response, http.StatusOK, toAccountResponse(account))
}

func (h *accountsHandler) deactivate(response http.ResponseWriter, request *http.Request) {
	id, ok := accountID(response, request.PathValue("id"))
	if !ok {
		return
	}
	err := h.repository.Deactivate(request.Context(), id, time.Now().UTC())
	if errors.Is(err, store.ErrAccountNotFound) {
		writeError(response, http.StatusNotFound, "account_not_found", "The account was not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "account_deactivate_failed", "The account could not be deactivated.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func accountID(response http.ResponseWriter, value string) (string, bool) {
	id, err := uuid.Parse(value)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_account_id", "Account ID must be a UUID.")
		return "", false
	}
	return id.String(), true
}

func safeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "" {
		return "background"
	}
	return name
}

func toAccountResponse(account domain.Account) accountResponse {
	backgroundID := ""
	if account.BackgroundAssetID != nil {
		backgroundID = *account.BackgroundAssetID
	}
	backgroundPath := ""
	if account.BackgroundPath != nil {
		backgroundPath = *account.BackgroundPath
	}
	return accountResponse{
		ID: account.ID, Name: account.Name, BackgroundAssetID: backgroundID, BackgroundPath: backgroundPath,
		Color: account.Color, Status: account.Status,
		CreatedAt: account.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: account.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"code": code, "message": message})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
