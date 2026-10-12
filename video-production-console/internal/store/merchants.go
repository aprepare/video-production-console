package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrMerchantNotFound    = errors.New("merchant not found")
	ErrMaterialNotFound    = errors.New("merchant material not found")
	ErrOrderNotFound       = errors.New("order not found")
	ErrCostNotFound        = errors.New("order cost not found")
	ErrInvalidMerchantData = errors.New("invalid merchant data")
)

type Merchant struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Category      string    `json:"category"`
	Contact       string    `json:"contact"`
	Phone         string    `json:"phone"`
	Address       string    `json:"address"`
	SellingPoints string    `json:"selling_points"`
	Notes         string    `json:"notes"`
	AccountID     string    `json:"account_id"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type MerchantMaterial struct {
	ID         string `json:"id"`
	MerchantID string `json:"merchant_id"`
	Kind       string `json:"kind"`
	Filename   string `json:"filename"`
	// Path is the absolute storage location; it never leaves the server.
	Path             string    `json:"-"`
	Size             int64     `json:"size"`
	SHA256           string    `json:"sha256"`
	Description      string    `json:"description"`
	LicenseSource    string    `json:"license_source"`
	LicenseScope     string    `json:"license_scope"`
	LicenseExpiresAt string    `json:"license_expires_at"`
	LicenseProof     string    `json:"license_proof"`
	ParentMaterialID string    `json:"parent_material_id"`
	ProvenanceJSON   string    `json:"provenance_json"`
	CreatedAt        time.Time `json:"created_at"`
}

type Order struct {
	ID             string    `json:"id"`
	MerchantID     string    `json:"merchant_id"`
	Title          string    `json:"title"`
	Package        string    `json:"package"`
	PriceCents     int64     `json:"price_cents"`
	PaidCents      int64     `json:"paid_cents"`
	VideoQuota     int       `json:"video_quota"`
	MaxDurationS   int       `json:"max_duration_s"`
	RevisionRounds int       `json:"revision_rounds"`
	DueDate        string    `json:"due_date"`
	ScopeNotes     string    `json:"scope_notes"`
	Status         string    `json:"status"`
	RenewalOf      string    `json:"renewal_of"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type OrderCost struct {
	ID          string    `json:"id"`
	OrderID     string    `json:"order_id"`
	ProjectID   string    `json:"project_id"`
	Kind        string    `json:"kind"`
	Minutes     int       `json:"minutes"`
	AmountCents int64     `json:"amount_cents"`
	Note        string    `json:"note"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type OrderProject struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Stage       string     `json:"stage"`
	AccountID   string     `json:"account_id"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
}

type OrderSummary struct {
	OrderID               string `json:"order_id"`
	PriceCents            int64  `json:"price_cents"`
	PaidCents             int64  `json:"paid_cents"`
	CostCents             int64  `json:"cost_cents"`
	LaborMinutes          int64  `json:"labor_minutes"`
	RevisionCount         int64  `json:"revision_count"`
	ProjectCount          int64  `json:"project_count"`
	DeliveredProjectCount int64  `json:"delivered_project_count"`
	ContributionCents     int64  `json:"contribution_cents"`
}

type BusinessOverview struct {
	MerchantsByStatus map[string]int64 `json:"merchants_by_status"`
	OrdersByStatus    map[string]int64 `json:"orders_by_status"`
	PriceCents        int64            `json:"price_cents"`
	PaidCents         int64            `json:"paid_cents"`
	CostCents         int64            `json:"cost_cents"`
	ContributionCents int64            `json:"contribution_cents"`
	RenewalCount      int64            `json:"renewal_count"`
}

type MerchantRepository struct{ db *sql.DB }

func NewMerchantRepository(db *sql.DB) *MerchantRepository { return &MerchantRepository{db: db} }

var (
	merchantStatuses = enumSet("prospect", "trial", "active", "paused", "churned")
	materialKinds    = enumSet("video", "image", "audio", "document", "other")
	licenseSources   = enumSet("merchant_shot", "team_shot", "licensed_stock", "ai_generated", "unknown")
	orderPackages    = enumSet("trial", "monthly", "premium", "custom")
	orderStatuses    = enumSet("draft", "signed", "producing", "delivered", "closed", "cancelled")
	costKinds        = enumSet("labor", "api", "material", "revision", "other")
)

func enumSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func invalidData(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidMerchantData, fmt.Sprintf(format, args...))
}

func validateEnum(field, value string, allowed map[string]struct{}) error {
	if _, ok := allowed[value]; !ok {
		return invalidData("invalid %s %q", field, value)
	}
	return nil
}

const merchantColumns = `id,name,category,contact,phone,address,selling_points,notes,account_id,status,created_at,updated_at`

func scanMerchant(row interface{ Scan(...any) error }) (Merchant, error) {
	var value Merchant
	err := row.Scan(&value.ID, &value.Name, &value.Category, &value.Contact, &value.Phone, &value.Address, &value.SellingPoints, &value.Notes, &value.AccountID, &value.Status, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

func (r *MerchantRepository) Create(ctx context.Context, value Merchant) (Merchant, error) {
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" {
		return Merchant{}, invalidData("merchant name is required")
	}
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	if value.Status == "" {
		value.Status = "prospect"
	}
	if err := validateEnum("merchant status", value.Status, merchantStatuses); err != nil {
		return Merchant{}, err
	}
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	} else {
		value.CreatedAt = value.CreatedAt.UTC()
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = now
	} else {
		value.UpdatedAt = value.UpdatedAt.UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO merchants(`+merchantColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.Name, value.Category, value.Contact, value.Phone, value.Address, value.SellingPoints, value.Notes, value.AccountID, value.Status, value.CreatedAt, value.UpdatedAt)
	if err != nil {
		return Merchant{}, fmt.Errorf("create merchant: %w", err)
	}
	return r.Get(ctx, value.ID)
}

func (r *MerchantRepository) Get(ctx context.Context, id string) (Merchant, error) {
	value, err := scanMerchant(r.db.QueryRowContext(ctx, `SELECT `+merchantColumns+` FROM merchants WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Merchant{}, ErrMerchantNotFound
	}
	if err != nil {
		return Merchant{}, fmt.Errorf("get merchant: %w", err)
	}
	return value, nil
}

func (r *MerchantRepository) List(ctx context.Context, status, query string) ([]Merchant, error) {
	if status != "" {
		if err := validateEnum("merchant status", status, merchantStatuses); err != nil {
			return nil, err
		}
	}
	where := []string{"1=1"}
	args := []any{}
	if status != "" {
		where = append(where, "status=?")
		args = append(args, status)
	}
	if query = strings.TrimSpace(query); query != "" {
		where = append(where, "(name LIKE ? OR category LIKE ? OR contact LIKE ?)")
		like := "%" + query + "%"
		args = append(args, like, like, like)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+merchantColumns+` FROM merchants WHERE `+strings.Join(where, " AND ")+` ORDER BY updated_at DESC,id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list merchants: %w", err)
	}
	defer rows.Close()
	items := []Merchant{}
	for rows.Next() {
		value, err := scanMerchant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan merchant: %w", err)
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate merchants: %w", err)
	}
	return items, nil
}

func (r *MerchantRepository) Update(ctx context.Context, value Merchant) (Merchant, error) {
	value.Name = strings.TrimSpace(value.Name)
	if value.ID == "" || value.Name == "" {
		return Merchant{}, invalidData("merchant id and name are required")
	}
	if err := validateEnum("merchant status", value.Status, merchantStatuses); err != nil {
		return Merchant{}, err
	}
	value.UpdatedAt = time.Now().UTC()
	result, err := r.db.ExecContext(ctx, `UPDATE merchants SET name=?,category=?,contact=?,phone=?,address=?,selling_points=?,notes=?,account_id=?,status=?,updated_at=? WHERE id=?`, value.Name, value.Category, value.Contact, value.Phone, value.Address, value.SellingPoints, value.Notes, value.AccountID, value.Status, value.UpdatedAt, value.ID)
	if err != nil {
		return Merchant{}, fmt.Errorf("update merchant: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return Merchant{}, ErrMerchantNotFound
	}
	return r.Get(ctx, value.ID)
}

func (r *MerchantRepository) Delete(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin merchant delete: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET order_id='',delivered_at=NULL,updated_at=? WHERE order_id IN (SELECT id FROM orders WHERE merchant_id=?)`, time.Now().UTC(), id); err != nil {
		return fmt.Errorf("unlink merchant projects: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM merchants WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete merchant: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrMerchantNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit merchant delete: %w", err)
	}
	return nil
}

const materialColumns = `id,merchant_id,kind,filename,path,size,sha256,description,license_source,license_scope,license_expires_at,license_proof,parent_material_id,provenance_json,created_at`

func scanMaterial(row interface{ Scan(...any) error }) (MerchantMaterial, error) {
	var value MerchantMaterial
	err := row.Scan(&value.ID, &value.MerchantID, &value.Kind, &value.Filename, &value.Path, &value.Size, &value.SHA256, &value.Description, &value.LicenseSource, &value.LicenseScope, &value.LicenseExpiresAt, &value.LicenseProof, &value.ParentMaterialID, &value.ProvenanceJSON, &value.CreatedAt)
	return value, err
}

func validateMaterial(value MerchantMaterial) error {
	if value.MerchantID == "" || strings.TrimSpace(value.Filename) == "" || strings.TrimSpace(value.Path) == "" {
		return invalidData("material merchant_id, filename and path are required")
	}
	if value.Size < 0 {
		return invalidData("material size cannot be negative")
	}
	if err := validateEnum("material kind", value.Kind, materialKinds); err != nil {
		return err
	}
	return validateEnum("material license_source", value.LicenseSource, licenseSources)
}

func (r *MerchantRepository) CreateMaterial(ctx context.Context, value MerchantMaterial) (MerchantMaterial, error) {
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	if value.Kind == "" {
		value.Kind = "other"
	}
	if value.LicenseSource == "" {
		value.LicenseSource = "merchant_shot"
	}
	if err := validateMaterial(value); err != nil {
		return MerchantMaterial{}, err
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now().UTC()
	} else {
		value.CreatedAt = value.CreatedAt.UTC()
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO merchant_materials(`+materialColumns+`) SELECT ?,id,?,?,?,?,?,?,?,?,?,?,?,?,? FROM merchants WHERE id=?`, value.ID, value.Kind, value.Filename, value.Path, value.Size, value.SHA256, value.Description, value.LicenseSource, value.LicenseScope, value.LicenseExpiresAt, value.LicenseProof, value.ParentMaterialID, value.ProvenanceJSON, value.CreatedAt, value.MerchantID)
	if err != nil {
		return MerchantMaterial{}, fmt.Errorf("create merchant material: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return MerchantMaterial{}, ErrMerchantNotFound
	}
	return r.GetMaterial(ctx, value.ID)
}

func (r *MerchantRepository) GetMaterial(ctx context.Context, id string) (MerchantMaterial, error) {
	value, err := scanMaterial(r.db.QueryRowContext(ctx, `SELECT `+materialColumns+` FROM merchant_materials WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return MerchantMaterial{}, ErrMaterialNotFound
	}
	if err != nil {
		return MerchantMaterial{}, fmt.Errorf("get merchant material: %w", err)
	}
	return value, nil
}

func (r *MerchantRepository) ListMaterials(ctx context.Context, merchantID string) ([]MerchantMaterial, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+materialColumns+` FROM merchant_materials WHERE merchant_id=? ORDER BY created_at DESC,id`, merchantID)
	if err != nil {
		return nil, fmt.Errorf("list merchant materials: %w", err)
	}
	defer rows.Close()
	items := []MerchantMaterial{}
	for rows.Next() {
		value, err := scanMaterial(rows)
		if err != nil {
			return nil, fmt.Errorf("scan merchant material: %w", err)
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate merchant materials: %w", err)
	}
	return items, nil
}

func (r *MerchantRepository) UpdateMaterial(ctx context.Context, value MerchantMaterial) (MerchantMaterial, error) {
	if value.ID == "" {
		return MerchantMaterial{}, invalidData("material id is required")
	}
	if err := validateEnum("material kind", value.Kind, materialKinds); err != nil {
		return MerchantMaterial{}, err
	}
	if err := validateEnum("material license_source", value.LicenseSource, licenseSources); err != nil {
		return MerchantMaterial{}, err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE merchant_materials SET kind=?,description=?,license_source=?,license_scope=?,license_expires_at=?,license_proof=? WHERE id=?`, value.Kind, value.Description, value.LicenseSource, value.LicenseScope, value.LicenseExpiresAt, value.LicenseProof, value.ID)
	if err != nil {
		return MerchantMaterial{}, fmt.Errorf("update merchant material: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return MerchantMaterial{}, ErrMaterialNotFound
	}
	return r.GetMaterial(ctx, value.ID)
}

func (r *MerchantRepository) DeleteMaterial(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM merchant_materials WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete merchant material: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrMaterialNotFound
	}
	return nil
}

const orderColumns = `id,merchant_id,title,package,price_cents,paid_cents,video_quota,max_duration_s,revision_rounds,due_date,scope_notes,status,renewal_of,created_at,updated_at`

func scanOrder(row interface{ Scan(...any) error }) (Order, error) {
	var value Order
	err := row.Scan(&value.ID, &value.MerchantID, &value.Title, &value.Package, &value.PriceCents, &value.PaidCents, &value.VideoQuota, &value.MaxDurationS, &value.RevisionRounds, &value.DueDate, &value.ScopeNotes, &value.Status, &value.RenewalOf, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

func validateOrder(value Order) error {
	if value.MerchantID == "" || strings.TrimSpace(value.Title) == "" {
		return invalidData("order merchant_id and title are required")
	}
	if value.PriceCents < 0 || value.PaidCents < 0 || value.VideoQuota < 0 || value.MaxDurationS < 0 || value.RevisionRounds < 0 {
		return invalidData("order numeric values cannot be negative")
	}
	if err := validateEnum("order package", value.Package, orderPackages); err != nil {
		return err
	}
	return validateEnum("order status", value.Status, orderStatuses)
}

func (r *MerchantRepository) CreateOrder(ctx context.Context, value Order) (Order, error) {
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	if value.Package == "" {
		value.Package = "custom"
	}
	if value.Status == "" {
		value.Status = "draft"
	}
	value.Title = strings.TrimSpace(value.Title)
	if err := validateOrder(value); err != nil {
		return Order{}, err
	}
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	} else {
		value.CreatedAt = value.CreatedAt.UTC()
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = now
	} else {
		value.UpdatedAt = value.UpdatedAt.UTC()
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO orders(`+orderColumns+`) SELECT ?,id,?,?,?,?,?,?,?,?,?,?,?,?,? FROM merchants WHERE id=?`, value.ID, value.Title, value.Package, value.PriceCents, value.PaidCents, value.VideoQuota, value.MaxDurationS, value.RevisionRounds, value.DueDate, value.ScopeNotes, value.Status, value.RenewalOf, value.CreatedAt, value.UpdatedAt, value.MerchantID)
	if err != nil {
		return Order{}, fmt.Errorf("create order: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return Order{}, ErrMerchantNotFound
	}
	return r.GetOrder(ctx, value.ID)
}

func (r *MerchantRepository) GetOrder(ctx context.Context, id string) (Order, error) {
	value, err := scanOrder(r.db.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("get order: %w", err)
	}
	return value, nil
}

func (r *MerchantRepository) ListOrders(ctx context.Context, merchantID, status string) ([]Order, error) {
	if status != "" {
		if err := validateEnum("order status", status, orderStatuses); err != nil {
			return nil, err
		}
	}
	where := []string{"1=1"}
	args := []any{}
	if merchantID != "" {
		where = append(where, "merchant_id=?")
		args = append(args, merchantID)
	}
	if status != "" {
		where = append(where, "status=?")
		args = append(args, status)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC,id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()
	items := []Order{}
	for rows.Next() {
		value, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orders: %w", err)
	}
	return items, nil
}

func (r *MerchantRepository) UpdateOrder(ctx context.Context, value Order) (Order, error) {
	if value.ID == "" {
		return Order{}, invalidData("order id is required")
	}
	value.Title = strings.TrimSpace(value.Title)
	if err := validateOrder(value); err != nil {
		return Order{}, err
	}
	value.UpdatedAt = time.Now().UTC()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, fmt.Errorf("begin order update: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM orders WHERE id=?`, value.ID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	} else if err != nil {
		return Order{}, fmt.Errorf("check order update target: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM merchants WHERE id=?`, value.MerchantID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrMerchantNotFound
	} else if err != nil {
		return Order{}, fmt.Errorf("check order merchant: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE orders SET merchant_id=?,title=?,package=?,price_cents=?,paid_cents=?,video_quota=?,max_duration_s=?,revision_rounds=?,due_date=?,scope_notes=?,status=?,renewal_of=?,updated_at=? WHERE id=?`, value.MerchantID, value.Title, value.Package, value.PriceCents, value.PaidCents, value.VideoQuota, value.MaxDurationS, value.RevisionRounds, value.DueDate, value.ScopeNotes, value.Status, value.RenewalOf, value.UpdatedAt, value.ID)
	if err != nil {
		return Order{}, fmt.Errorf("update order: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return Order{}, ErrOrderNotFound
	}
	if err := tx.Commit(); err != nil {
		return Order{}, fmt.Errorf("commit order update: %w", err)
	}
	return r.GetOrder(ctx, value.ID)
}

func (r *MerchantRepository) DeleteOrder(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin order delete: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET order_id='',delivered_at=NULL,updated_at=? WHERE order_id=?`, time.Now().UTC(), id); err != nil {
		return fmt.Errorf("unlink order projects: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM orders WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete order: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrOrderNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit order delete: %w", err)
	}
	return nil
}

const costColumns = `id,order_id,project_id,kind,minutes,amount_cents,note,occurred_at`

func scanCost(row interface{ Scan(...any) error }) (OrderCost, error) {
	var value OrderCost
	err := row.Scan(&value.ID, &value.OrderID, &value.ProjectID, &value.Kind, &value.Minutes, &value.AmountCents, &value.Note, &value.OccurredAt)
	return value, err
}

func (r *MerchantRepository) CreateCost(ctx context.Context, value OrderCost) (OrderCost, error) {
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	if value.Kind == "" {
		value.Kind = "other"
	}
	if value.OrderID == "" {
		return OrderCost{}, invalidData("cost order_id is required")
	}
	if value.Minutes < 0 || value.AmountCents < 0 {
		return OrderCost{}, invalidData("cost numeric values cannot be negative")
	}
	if err := validateEnum("cost kind", value.Kind, costKinds); err != nil {
		return OrderCost{}, err
	}
	if value.OccurredAt.IsZero() {
		value.OccurredAt = time.Now().UTC()
	} else {
		value.OccurredAt = value.OccurredAt.UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return OrderCost{}, fmt.Errorf("begin cost create: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM orders WHERE id=?`, value.OrderID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return OrderCost{}, ErrOrderNotFound
	} else if err != nil {
		return OrderCost{}, fmt.Errorf("check cost order: %w", err)
	}
	if value.ProjectID != "" {
		var linkedOrder string
		if err := tx.QueryRowContext(ctx, `SELECT order_id FROM projects WHERE id=?`, value.ProjectID).Scan(&linkedOrder); errors.Is(err, sql.ErrNoRows) {
			return OrderCost{}, ErrProjectNotFound
		} else if err != nil {
			return OrderCost{}, fmt.Errorf("check cost project: %w", err)
		} else if linkedOrder != value.OrderID {
			return OrderCost{}, invalidData("project %s does not belong to order %s", value.ProjectID, value.OrderID)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO order_costs(`+costColumns+`) VALUES(?,?,?,?,?,?,?,?)`, value.ID, value.OrderID, value.ProjectID, value.Kind, value.Minutes, value.AmountCents, value.Note, value.OccurredAt); err != nil {
		return OrderCost{}, fmt.Errorf("create order cost: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return OrderCost{}, fmt.Errorf("commit order cost: %w", err)
	}
	return value, nil
}

func (r *MerchantRepository) ListCosts(ctx context.Context, orderID string) ([]OrderCost, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+costColumns+` FROM order_costs WHERE order_id=? ORDER BY occurred_at DESC,id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list order costs: %w", err)
	}
	defer rows.Close()
	items := []OrderCost{}
	for rows.Next() {
		value, err := scanCost(rows)
		if err != nil {
			return nil, fmt.Errorf("scan order cost: %w", err)
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order costs: %w", err)
	}
	return items, nil
}

func (r *MerchantRepository) DeleteCost(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM order_costs WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete order cost: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrCostNotFound
	}
	return nil
}

func (r *MerchantRepository) AssignProjectToOrder(ctx context.Context, projectID, orderID string) error {
	if projectID == "" {
		return invalidData("project id is required")
	}
	if orderID == "" {
		result, err := r.db.ExecContext(ctx, `UPDATE projects SET order_id='',delivered_at=NULL,updated_at=? WHERE id=?`, time.Now().UTC(), projectID)
		if err != nil {
			return fmt.Errorf("unassign project: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return ErrProjectNotFound
		}
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin project assignment: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM orders WHERE id=?`, orderID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrOrderNotFound
	} else if err != nil {
		return fmt.Errorf("check assignment order: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE projects SET order_id=?,delivered_at=CASE WHEN order_id=? THEN delivered_at ELSE NULL END,updated_at=? WHERE id=?`, orderID, orderID, time.Now().UTC(), projectID)
	if err != nil {
		return fmt.Errorf("assign project to order: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrProjectNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project assignment: %w", err)
	}
	return nil
}

func (r *MerchantRepository) ListOrderProjects(ctx context.Context, orderID string) ([]OrderProject, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,title,stage,account_id,updated_at,delivered_at FROM projects WHERE order_id=? ORDER BY updated_at DESC,id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list order projects: %w", err)
	}
	defer rows.Close()
	items := []OrderProject{}
	for rows.Next() {
		var value OrderProject
		if err := rows.Scan(&value.ID, &value.Title, &value.Stage, &value.AccountID, &value.UpdatedAt, &value.DeliveredAt); err != nil {
			return nil, fmt.Errorf("scan order project: %w", err)
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order projects: %w", err)
	}
	return items, nil
}

func (r *MerchantRepository) MarkProjectDelivered(ctx context.Context, projectID string, deliveredAt time.Time) error {
	if projectID == "" || deliveredAt.IsZero() {
		return invalidData("project id and delivery time are required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE projects SET delivered_at=?,updated_at=? WHERE id=?`, deliveredAt.UTC(), time.Now().UTC(), projectID)
	if err != nil {
		return fmt.Errorf("mark project delivered: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrProjectNotFound
	}
	return nil
}

func (r *MerchantRepository) UnassignOrderProject(ctx context.Context, orderID, projectID string) error {
	if orderID == "" || projectID == "" {
		return invalidData("order id and project id are required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE projects SET order_id='',delivered_at=NULL,updated_at=? WHERE id=? AND order_id=?`, time.Now().UTC(), projectID, orderID)
	if err != nil {
		return fmt.Errorf("unassign order project: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrProjectNotFound
	}
	return nil
}

func (r *MerchantRepository) DeliverOrderProject(ctx context.Context, orderID, projectID string, deliveredAt time.Time) error {
	if orderID == "" || projectID == "" || deliveredAt.IsZero() {
		return invalidData("order id, project id and delivery time are required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE projects SET delivered_at=?,updated_at=? WHERE id=? AND order_id=?`, deliveredAt.UTC(), time.Now().UTC(), projectID, orderID)
	if err != nil {
		return fmt.Errorf("deliver order project: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrProjectNotFound
	}
	return nil
}

func (r *MerchantRepository) OrderSummary(ctx context.Context, orderID string) (OrderSummary, error) {
	var value OrderSummary
	err := r.db.QueryRowContext(ctx, `SELECT o.id,o.price_cents,o.paid_cents,
		COALESCE((SELECT SUM(c.amount_cents) FROM order_costs c WHERE c.order_id=o.id),0),
		COALESCE((SELECT SUM(c.minutes) FROM order_costs c WHERE c.order_id=o.id AND c.kind='labor'),0),
		(SELECT COUNT(*) FROM order_costs c WHERE c.order_id=o.id AND c.kind='revision'),
		(SELECT COUNT(*) FROM projects p WHERE p.order_id=o.id),
		(SELECT COUNT(*) FROM projects p WHERE p.order_id=o.id AND p.delivered_at IS NOT NULL)
		FROM orders o WHERE o.id=?`, orderID).Scan(&value.OrderID, &value.PriceCents, &value.PaidCents, &value.CostCents, &value.LaborMinutes, &value.RevisionCount, &value.ProjectCount, &value.DeliveredProjectCount)
	if errors.Is(err, sql.ErrNoRows) {
		return OrderSummary{}, ErrOrderNotFound
	}
	if err != nil {
		return OrderSummary{}, fmt.Errorf("summarize order: %w", err)
	}
	value.ContributionCents = value.PaidCents - value.CostCents
	return value, nil
}

func (r *MerchantRepository) Overview(ctx context.Context) (BusinessOverview, error) {
	value := BusinessOverview{
		MerchantsByStatus: make(map[string]int64, len(merchantStatuses)),
		OrdersByStatus:    make(map[string]int64, len(orderStatuses)),
	}
	for status := range merchantStatuses {
		value.MerchantsByStatus[status] = 0
	}
	for status := range orderStatuses {
		value.OrdersByStatus[status] = 0
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return BusinessOverview{}, fmt.Errorf("begin business overview: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT status,COUNT(*) FROM merchants GROUP BY status`)
	if err != nil {
		return BusinessOverview{}, fmt.Errorf("count merchants by status: %w", err)
	}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return BusinessOverview{}, fmt.Errorf("scan merchant status count: %w", err)
		}
		value.MerchantsByStatus[status] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return BusinessOverview{}, fmt.Errorf("iterate merchant status counts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return BusinessOverview{}, fmt.Errorf("close merchant status counts: %w", err)
	}
	rows, err = tx.QueryContext(ctx, `SELECT status,COUNT(*) FROM orders GROUP BY status`)
	if err != nil {
		return BusinessOverview{}, fmt.Errorf("count orders by status: %w", err)
	}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return BusinessOverview{}, fmt.Errorf("scan order status count: %w", err)
		}
		value.OrdersByStatus[status] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return BusinessOverview{}, fmt.Errorf("iterate order status counts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return BusinessOverview{}, fmt.Errorf("close order status counts: %w", err)
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(price_cents),0),COALESCE(SUM(paid_cents),0),COUNT(*) FILTER (WHERE renewal_of<>'') FROM orders`).Scan(&value.PriceCents, &value.PaidCents, &value.RenewalCount)
	if err != nil {
		return BusinessOverview{}, fmt.Errorf("sum orders overview: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM order_costs`).Scan(&value.CostCents); err != nil {
		return BusinessOverview{}, fmt.Errorf("sum costs overview: %w", err)
	}
	value.ContributionCents = value.PaidCents - value.CostCents
	if err := tx.Commit(); err != nil {
		return BusinessOverview{}, fmt.Errorf("commit business overview: %w", err)
	}
	return value, nil
}
