package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"log"
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
	repository accountStore
	assets     *assets.Service
}

type accountStore interface {
	List(context.Context) ([]domain.Account, error)
	CreateWithBackground(context.Context, domain.Account, store.NewBackground) (store.CommitState, error)
	Get(context.Context, string) (domain.Account, error)
	Rename(context.Context, string, string, time.Time) (domain.Account, error)
	ReplaceBackground(context.Context, string, store.NewBackground, time.Time) (domain.Account, store.CommitState, error)
	Deactivate(context.Context, string, time.Time) error
}

const maxMultipartRequestSize = assets.MaxBackgroundSize + (1 << 20)

type accountResponse struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	BackgroundAssetID string `json:"background_asset_id"`
	Color             string `json:"color"`
	Status            string `json:"status"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

func NewAccountsHandler(db *sql.DB, assetService *assets.Service) http.Handler {
	return newAccountsHandler(store.NewAccountRepository(db), assetService)
}

func newAccountsHandler(repository accountStore, assetService *assets.Service) http.Handler {
	handler := &accountsHandler{repository: repository, assets: assetService}
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
	if !parseMultipart(response, request) {
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
		writeUploadError(response, err)
		return
	}
	assetID := uuid.NewString()
	now := time.Now().UTC()
	account := domain.Account{
		ID: accountID, Name: name, BackgroundAssetID: &assetID,
		BackgroundPath: &saved.Path,
		Color:          "#5B8FF9", Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	state, err := h.repository.CreateWithBackground(request.Context(), account, store.NewBackground{
		ID: assetID, Path: saved.Path, Filename: safeFilename(header.Filename), MIMEType: saved.MIMEType,
		Size: saved.Size, SHA256: saved.SHA256,
	})
	if err != nil {
		if state == store.CommitNotCommitted {
			if removeErr := os.Remove(saved.Path); removeErr != nil {
				log.Printf("remove uncommitted account background: %v", removeErr)
			}
		}
		if state == store.CommitUnknown {
			writeError(response, http.StatusServiceUnavailable, "account_commit_unknown", "The account may have been created. Refresh before retrying.")
			return
		}
		if errors.Is(err, store.ErrAccountNameConflict) {
			writeError(response, http.StatusConflict, "account_name_conflict", "An active account with this name already exists.")
			return
		}
		log.Printf("create account: %v", err)
		writeError(response, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	stored, readErr := h.repository.Get(request.Context(), account.ID)
	if readErr != nil {
		log.Printf("read created account: %v", readErr)
		writeError(response, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	writeJSON(response, http.StatusCreated, toAccountResponse(stored))
}

func (h *accountsHandler) rename(response http.ResponseWriter, request *http.Request) {
	id, ok := accountID(response, request.PathValue("id"))
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(response, request, maxNormalJSONRequest, &input); err != nil {
		writeDecodeError(response, err, "invalid_json", "A JSON object with a name is required.")
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
	if !parseMultipart(response, request) {
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
		writeUploadError(response, err)
		return
	}
	background := store.NewBackground{
		ID: uuid.NewString(), Path: saved.Path, Filename: safeFilename(header.Filename), MIMEType: saved.MIMEType,
		Size: saved.Size, SHA256: saved.SHA256,
	}
	account, state, err := h.repository.ReplaceBackground(request.Context(), id, background, time.Now().UTC())
	if err != nil {
		if state == store.CommitNotCommitted {
			if removeErr := os.Remove(saved.Path); removeErr != nil {
				log.Printf("remove uncommitted replacement background: %v", removeErr)
			}
		}
		if state == store.CommitUnknown {
			writeError(response, http.StatusServiceUnavailable, "account_commit_unknown", "The background may have been replaced. Refresh before retrying.")
			return
		}
		if errors.Is(err, store.ErrAccountNotFound) {
			writeError(response, http.StatusNotFound, "account_not_found", "The account was not found.")
			return
		}
		log.Printf("replace account background: %v", err)
		writeError(response, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
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

func parseMultipart(response http.ResponseWriter, request *http.Request) bool {
	request.Body = http.MaxBytesReader(response, request.Body, maxMultipartRequestSize)
	if err := request.ParseMultipartForm(maxMultipartRequestSize); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(response, http.StatusRequestEntityTooLarge, "payload_too_large", "The upload is too large.")
			return false
		}
		writeError(response, http.StatusBadRequest, "invalid_multipart", "The multipart form could not be read.")
		return false
	}
	return true
}

func writeUploadError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, assets.ErrBackgroundTooBig):
		writeError(response, http.StatusRequestEntityTooLarge, "payload_too_large", "The upload is too large.")
	case errors.Is(err, assets.ErrImageDimensions):
		writeError(response, http.StatusBadRequest, "image_dimensions_exceeded", "Image dimensions exceed the allowed limits.")
	case errors.Is(err, assets.ErrInvalidImage), errors.Is(err, assets.ErrInvalidAccountID):
		writeError(response, http.StatusBadRequest, "invalid_background", "Background must be a valid PNG, JPEG, or WebP image.")
	default:
		log.Printf("save account background: %v", err)
		writeError(response, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
	}
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
	return accountResponse{
		ID: account.ID, Name: account.Name, BackgroundAssetID: backgroundID,
		Color: account.Color, Status: account.Status,
		CreatedAt: account.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: account.UpdatedAt.Format(time.RFC3339Nano),
	}
}
