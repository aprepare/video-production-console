package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/store"
)

const maxMerchantMaterialBytes int64 = 2 << 30

type merchantsHandler struct {
	repo         *store.MerchantRepository
	materialRoot string
}

func NewMerchantsHandler(db *sql.DB, dataRoot string) http.Handler {
	h := &merchantsHandler{repo: store.NewMerchantRepository(db), materialRoot: filepath.Join(dataRoot, "merchants")}
	mux := http.NewServeMux()
	routes := map[string]http.HandlerFunc{
		"GET /api/merchants":                                 h.listMerchants,
		"POST /api/merchants":                                h.createMerchant,
		"GET /api/merchants/{id}":                            h.getMerchant,
		"PATCH /api/merchants/{id}":                          h.updateMerchant,
		"DELETE /api/merchants/{id}":                         h.deleteMerchant,
		"GET /api/merchants/{id}/materials":                  h.listMaterials,
		"POST /api/merchants/{id}/materials":                 h.uploadMaterial,
		"PATCH /api/materials/{id}":                          h.updateMaterial,
		"DELETE /api/materials/{id}":                         h.deleteMaterial,
		"GET /api/materials/{id}/content":                    h.materialContent,
		"GET /api/orders":                                    h.listOrders,
		"POST /api/orders":                                   h.createOrder,
		"GET /api/orders/{id}":                               h.getOrder,
		"PATCH /api/orders/{id}":                             h.updateOrder,
		"DELETE /api/orders/{id}":                            h.deleteOrder,
		"POST /api/orders/{id}/costs":                        h.createCost,
		"DELETE /api/order-costs/{id}":                       h.deleteCost,
		"POST /api/orders/{id}/projects":                     h.assignProject,
		"DELETE /api/orders/{id}/projects/{projectID}":       h.unassignProject,
		"POST /api/orders/{id}/projects/{projectID}/deliver": h.deliverProject,
		"GET /api/business/overview":                         h.overview,
	}
	for pattern, handler := range routes {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			for _, key := range []string{"id", "projectID"} {
				if value := r.PathValue(key); value != "" {
					id, err := uuid.Parse(value)
					if err != nil {
						writeError(w, 400, "invalid_id", "标识格式不正确。")
						return
					}
					r.SetPathValue(key, id.String())
				}
			}
			handler(w, r)
		})
	}
	return mux
}

func merchantError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalidMerchantData):
		writeError(w, 400, "invalid_merchant_data", "字段值不正确，请检查必填项、金额和枚举值。")
	case errors.Is(err, store.ErrMerchantNotFound):
		writeError(w, 404, "merchant_not_found", "商户不存在。")
	case errors.Is(err, store.ErrMaterialNotFound):
		writeError(w, 404, "material_not_found", "素材不存在。")
	case errors.Is(err, store.ErrOrderNotFound):
		writeError(w, 404, "order_not_found", "订单不存在。")
	case errors.Is(err, store.ErrCostNotFound):
		writeError(w, 404, "cost_not_found", "成本记录不存在。")
	case errors.Is(err, store.ErrProjectNotFound):
		writeError(w, 404, "project_not_found", "项目不存在或不属于该订单。")
	default:
		writeError(w, 500, "merchant_operation_failed", "商户交付数据操作失败。")
	}
}

func merchantDecode(w http.ResponseWriter, r *http.Request, in any) bool {
	if err := decodeJSON(w, r, maxNormalJSONRequest, in); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "请求内容过大。")
			return false
		}
		writeDecodeError(w, err, "invalid_merchant_data", "请求内容格式不正确。")
		return false
	}
	return true
}

func merchantOptionalID(w http.ResponseWriter, value *string) bool {
	if *value == "" {
		return true
	}
	parsed, err := uuid.Parse(*value)
	if err != nil {
		writeError(w, 400, "invalid_id", "标识格式不正确。")
		return false
	}
	*value = parsed.String()
	return true
}

// Request types expose only operator-editable fields, so PATCH cannot replace IDs or timestamps.
type merchantInput struct {
	Name          string `json:"name"`
	Category      string `json:"category"`
	Contact       string `json:"contact"`
	Phone         string `json:"phone"`
	Address       string `json:"address"`
	SellingPoints string `json:"selling_points"`
	Notes         string `json:"notes"`
	AccountID     string `json:"account_id"`
	Status        string `json:"status"`
}

func (in merchantInput) merchant(id string) store.Merchant {
	return store.Merchant{ID: id, Name: in.Name, Category: in.Category, Contact: in.Contact, Phone: in.Phone, Address: in.Address, SellingPoints: in.SellingPoints, Notes: in.Notes, AccountID: in.AccountID, Status: in.Status}
}

func (h *merchantsHandler) listMerchants(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.List(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("q"))
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
func (h *merchantsHandler) createMerchant(w http.ResponseWriter, r *http.Request) {
	in := merchantInput{Status: "prospect"}
	if !merchantDecode(w, r, &in) || !merchantOptionalID(w, &in.AccountID) {
		return
	}
	// Explicit empty enum values are invalid; omitted values use the schema default.
	if in.Status == "" {
		merchantError(w, store.ErrInvalidMerchantData)
		return
	}
	item, err := h.repo.Create(r.Context(), in.merchant(uuid.NewString()))
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 201, item)
}
func (h *merchantsHandler) getMerchant(w http.ResponseWriter, r *http.Request) {
	item, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 200, item)
}
func (h *merchantsHandler) updateMerchant(w http.ResponseWriter, r *http.Request) {
	current, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		merchantError(w, err)
		return
	}
	in := merchantInput{Name: current.Name, Category: current.Category, Contact: current.Contact, Phone: current.Phone, Address: current.Address, SellingPoints: current.SellingPoints, Notes: current.Notes, AccountID: current.AccountID, Status: current.Status}
	if !merchantDecode(w, r, &in) || !merchantOptionalID(w, &in.AccountID) {
		return
	}
	item, err := h.repo.Update(r.Context(), in.merchant(current.ID))
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 200, item)
}
func (h *merchantsHandler) deleteMerchant(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	materials, err := h.repo.ListMaterials(r.Context(), id)
	if err != nil {
		merchantError(w, err)
		return
	}
	// Validate all paths before deleting anything in a cascading operation.
	for _, m := range materials {
		if _, err := h.safeMaterialPath(m.Path); err != nil {
			materialFileError(w, err)
			return
		}
	}
	for _, m := range materials {
		if err := h.removeMaterialFile(m.Path); err != nil {
			materialFileError(w, err)
			return
		}
	}
	if err := h.repo.Delete(r.Context(), id); err != nil {
		merchantError(w, err)
		return
	}
	w.WriteHeader(204)
}

type orderInput struct {
	MerchantID     string `json:"merchant_id"`
	Title          string `json:"title"`
	Package        string `json:"package"`
	PriceCents     int64  `json:"price_cents"`
	PaidCents      int64  `json:"paid_cents"`
	VideoQuota     int    `json:"video_quota"`
	MaxDurationS   int    `json:"max_duration_s"`
	RevisionRounds int    `json:"revision_rounds"`
	DueDate        string `json:"due_date"`
	ScopeNotes     string `json:"scope_notes"`
	Status         string `json:"status"`
	RenewalOf      string `json:"renewal_of"`
}

func (in orderInput) order(id string) store.Order {
	return store.Order{ID: id, MerchantID: in.MerchantID, Title: in.Title, Package: in.Package, PriceCents: in.PriceCents, PaidCents: in.PaidCents, VideoQuota: in.VideoQuota, MaxDurationS: in.MaxDurationS, RevisionRounds: in.RevisionRounds, DueDate: in.DueDate, ScopeNotes: in.ScopeNotes, Status: in.Status, RenewalOf: in.RenewalOf}
}
func (in *orderInput) validIDs(w http.ResponseWriter) bool {
	if in.MerchantID == "" || in.Package == "" || in.Status == "" {
		merchantError(w, store.ErrInvalidMerchantData)
		return false
	}
	return merchantOptionalID(w, &in.MerchantID) && merchantOptionalID(w, &in.RenewalOf)
}
func (h *merchantsHandler) listOrders(w http.ResponseWriter, r *http.Request) {
	mid := r.URL.Query().Get("merchant_id")
	if !merchantOptionalID(w, &mid) {
		return
	}
	items, err := h.repo.ListOrders(r.Context(), mid, r.URL.Query().Get("status"))
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
func (h *merchantsHandler) createOrder(w http.ResponseWriter, r *http.Request) {
	in := orderInput{Package: "custom", Status: "draft"}
	if !merchantDecode(w, r, &in) || !in.validIDs(w) {
		return
	}
	item, err := h.repo.CreateOrder(r.Context(), in.order(uuid.NewString()))
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 201, item)
}
func (h *merchantsHandler) getOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, err := h.repo.GetOrder(r.Context(), id)
	if err != nil {
		merchantError(w, err)
		return
	}
	summary, err := h.repo.OrderSummary(r.Context(), id)
	if err != nil {
		merchantError(w, err)
		return
	}
	costs, err := h.repo.ListCosts(r.Context(), id)
	if err != nil {
		merchantError(w, err)
		return
	}
	projects, err := h.repo.ListOrderProjects(r.Context(), id)
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"order": order, "summary": summary, "costs": costs, "projects": projects})
}
func (h *merchantsHandler) updateOrder(w http.ResponseWriter, r *http.Request) {
	c, err := h.repo.GetOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		merchantError(w, err)
		return
	}
	in := orderInput{MerchantID: c.MerchantID, Title: c.Title, Package: c.Package, PriceCents: c.PriceCents, PaidCents: c.PaidCents, VideoQuota: c.VideoQuota, MaxDurationS: c.MaxDurationS, RevisionRounds: c.RevisionRounds, DueDate: c.DueDate, ScopeNotes: c.ScopeNotes, Status: c.Status, RenewalOf: c.RenewalOf}
	if !merchantDecode(w, r, &in) || !in.validIDs(w) {
		return
	}
	item, err := h.repo.UpdateOrder(r.Context(), in.order(c.ID))
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 200, item)
}
func (h *merchantsHandler) deleteOrder(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.DeleteOrder(r.Context(), r.PathValue("id")); err != nil {
		merchantError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *merchantsHandler) createCost(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID   string    `json:"project_id"`
		Kind        string    `json:"kind"`
		Minutes     int       `json:"minutes"`
		AmountCents int64     `json:"amount_cents"`
		Note        string    `json:"note"`
		OccurredAt  time.Time `json:"occurred_at"`
	}
	if !merchantDecode(w, r, &in) || !merchantOptionalID(w, &in.ProjectID) {
		return
	}
	if in.Kind == "" {
		merchantError(w, store.ErrInvalidMerchantData)
		return
	}
	item, err := h.repo.CreateCost(r.Context(), store.OrderCost{ID: uuid.NewString(), OrderID: r.PathValue("id"), ProjectID: in.ProjectID, Kind: in.Kind, Minutes: in.Minutes, AmountCents: in.AmountCents, Note: in.Note, OccurredAt: in.OccurredAt})
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 201, item)
}
func (h *merchantsHandler) deleteCost(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.DeleteCost(r.Context(), r.PathValue("id")); err != nil {
		merchantError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *merchantsHandler) assignProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID string `json:"project_id"`
	}
	if !merchantDecode(w, r, &in) {
		return
	}
	if in.ProjectID == "" {
		merchantError(w, store.ErrInvalidMerchantData)
		return
	}
	if !merchantOptionalID(w, &in.ProjectID) {
		return
	}
	if err := h.repo.AssignProjectToOrder(r.Context(), in.ProjectID, r.PathValue("id")); err != nil {
		merchantError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *merchantsHandler) unassignProject(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.UnassignOrderProject(r.Context(), r.PathValue("id"), r.PathValue("projectID")); err != nil {
		merchantError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *merchantsHandler) deliverProject(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.DeliverOrderProject(r.Context(), r.PathValue("id"), r.PathValue("projectID"), time.Now().UTC()); err != nil {
		merchantError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *merchantsHandler) overview(w http.ResponseWriter, r *http.Request) {
	item, err := h.repo.Overview(r.Context())
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

func (h *merchantsHandler) listMaterials(w http.ResponseWriter, r *http.Request) {
	if _, err := h.repo.Get(r.Context(), r.PathValue("id")); err != nil {
		merchantError(w, err)
		return
	}
	items, err := h.repo.ListMaterials(r.Context(), r.PathValue("id"))
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
func (h *merchantsHandler) updateMaterial(w http.ResponseWriter, r *http.Request) {
	current, err := h.repo.GetMaterial(r.Context(), r.PathValue("id"))
	if err != nil {
		merchantError(w, err)
		return
	}
	in := struct {
		Kind             string `json:"kind"`
		Description      string `json:"description"`
		LicenseSource    string `json:"license_source"`
		LicenseScope     string `json:"license_scope"`
		LicenseExpiresAt string `json:"license_expires_at"`
		LicenseProof     string `json:"license_proof"`
	}{current.Kind, current.Description, current.LicenseSource, current.LicenseScope, current.LicenseExpiresAt, current.LicenseProof}
	if !merchantDecode(w, r, &in) {
		return
	}
	current.Kind, current.Description, current.LicenseSource, current.LicenseScope, current.LicenseExpiresAt, current.LicenseProof = in.Kind, in.Description, in.LicenseSource, in.LicenseScope, in.LicenseExpiresAt, in.LicenseProof
	item, err := h.repo.UpdateMaterial(r.Context(), current)
	if err != nil {
		merchantError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

func inferredMerchantMaterialKind(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".mp4", ".mov":
		return "video"
	case ".jpg", ".jpeg", ".png", ".webp":
		return "image"
	case ".mp3", ".wav", ".m4a":
		return "audio"
	case ".pdf", ".doc", ".docx", ".txt":
		return "document"
	default:
		return "other"
	}
}

func (h *merchantsHandler) uploadMaterial(w http.ResponseWriter, r *http.Request) {
	mid := r.PathValue("id")
	if _, err := h.repo.Get(r.Context(), mid); err != nil {
		merchantError(w, err)
		return
	}
	// Allow multipart headers/metadata in addition to the per-file limit.
	r.Body = http.MaxBytesReader(w, r.Body, maxMerchantMaterialBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(w, 413, "material_too_large", "素材文件不能超过 2 GiB。")
		} else {
			writeError(w, 400, "invalid_multipart", "请提供有效的素材上传表单。")
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	if len(r.MultipartForm.File["file"]) != 1 || len(r.MultipartForm.File) != 1 {
		writeError(w, 400, "invalid_file", "每次请上传一个素材文件。")
		return
	}
	source, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "invalid_file", "缺少素材文件。")
		return
	}
	defer source.Close()
	if header.Size > maxMerchantMaterialBytes {
		writeError(w, 413, "material_too_large", "素材文件不能超过 2 GiB。")
		return
	}
	filename := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	kind := r.FormValue("kind")
	if kind == "" {
		kind = inferredMerchantMaterialKind(filename)
	}
	license := r.FormValue("license_source")
	if license == "" {
		license = "merchant_shot"
	}
	switch kind {
	case "video", "image", "audio", "document", "other":
	default:
		merchantError(w, store.ErrInvalidMerchantData)
		return
	}
	switch license {
	case "merchant_shot", "team_shot", "licensed_stock", "ai_generated", "unknown":
	default:
		merchantError(w, store.ErrInvalidMerchantData)
		return
	}
	id := uuid.NewString()
	path, err := filepath.Abs(filepath.Join(h.materialRoot, mid, "materials", id+filepath.Ext(filename)))
	if err != nil {
		materialFileError(w, err)
		return
	}
	if _, err := h.safeMaterialPath(path); err != nil {
		materialFileError(w, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		materialFileError(w, err)
		return
	}
	if _, err := h.safeMaterialPath(path); err != nil {
		materialFileError(w, err)
		return
	}
	dest, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		materialFileError(w, err)
		return
	}
	kept := false
	defer func() {
		dest.Close()
		if !kept {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(dest, hash), io.LimitReader(source, maxMerchantMaterialBytes+1))
	closeErr := dest.Close()
	if err != nil {
		materialFileError(w, err)
		return
	}
	if closeErr != nil {
		materialFileError(w, closeErr)
		return
	}
	if size > maxMerchantMaterialBytes {
		writeError(w, 413, "material_too_large", "素材文件不能超过 2 GiB。")
		return
	}
	item, err := h.repo.CreateMaterial(r.Context(), store.MerchantMaterial{ID: id, MerchantID: mid, Kind: kind, Filename: filename, Path: path, Size: size, SHA256: fmt.Sprintf("%x", hash.Sum(nil)), Description: r.FormValue("description"), LicenseSource: license, LicenseScope: r.FormValue("license_scope"), LicenseExpiresAt: r.FormValue("license_expires_at"), LicenseProof: r.FormValue("license_proof")})
	if err != nil {
		merchantError(w, err)
		return
	}
	kept = true
	writeJSON(w, 201, item)
}

var errUnsafeMaterialPath = errors.New("material path escapes storage")

// Check both the lexical boundary and existing links, including directory junctions.
func (h *merchantsHandler) safeMaterialPath(path string) (string, error) {
	root, err := filepath.Abs(h.materialRoot)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errUnsafeMaterialPath
	}
	current := root
	components := append([]string{""}, strings.Split(rel, string(filepath.Separator))...)
	for _, part := range components {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errUnsafeMaterialPath
		}
	}
	return target, nil
}
func materialFileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errUnsafeMaterialPath):
		writeError(w, 400, "unsafe_material_path", "素材路径不在允许的存储目录内。")
	case os.IsNotExist(err):
		writeError(w, 404, "material_file_not_found", "素材文件不存在。")
	default:
		writeError(w, 500, "material_file_failed", "素材文件操作失败。")
	}
}
func (h *merchantsHandler) removeMaterialFile(path string) error {
	safe, err := h.safeMaterialPath(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(safe)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errUnsafeMaterialPath
	}
	if err := os.Remove(safe); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
func (h *merchantsHandler) deleteMaterial(w http.ResponseWriter, r *http.Request) {
	item, err := h.repo.GetMaterial(r.Context(), r.PathValue("id"))
	if err != nil {
		merchantError(w, err)
		return
	}
	if err := h.removeMaterialFile(item.Path); err != nil {
		materialFileError(w, err)
		return
	}
	if err := h.repo.DeleteMaterial(r.Context(), item.ID); err != nil {
		merchantError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *merchantsHandler) materialContent(w http.ResponseWriter, r *http.Request) {
	item, err := h.repo.GetMaterial(r.Context(), r.PathValue("id"))
	if err != nil {
		merchantError(w, err)
		return
	}
	path, err := h.safeMaterialPath(item.Path)
	if err != nil {
		materialFileError(w, err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		materialFileError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		materialFileError(w, err)
		return
	}
	if !info.Mode().IsRegular() {
		materialFileError(w, errUnsafeMaterialPath)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, item.Filename, info.ModTime(), file)
}
