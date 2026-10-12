package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMerchantRepositoryCRUDAndFilters(t *testing.T) {
	repo, _, _ := merchantFixture(t)
	ctx := context.Background()
	created, err := repo.Create(ctx, Merchant{Name: "岚山茶叶", Category: "茶叶", Contact: "林经理", Phone: "123", Address: "杭州", SellingPoints: "春茶", Notes: "重点", AccountID: "account-a"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != "prospect" || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("Create() = %+v", created)
	}
	created.Status = "active"
	created.Name = "岚山新茶"
	updated, err := repo.Update(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "岚山新茶" || updated.Status != "active" || updated.Category != "茶叶" || updated.AccountID != "account-a" {
		t.Fatalf("Update() = %+v", updated)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("Get() = %+v, %v", got, err)
	}
	for _, filter := range []struct{ status, query string }{{"active", ""}, {"", "岚山"}, {"", "茶叶"}, {"", "林经理"}} {
		items, err := repo.List(ctx, filter.status, filter.query)
		if err != nil || len(items) != 1 || items[0].ID != created.ID {
			t.Fatalf("List(%q,%q) = %+v, %v", filter.status, filter.query, items, err)
		}
	}
	items, err := repo.List(ctx, "paused", "")
	if err != nil || len(items) != 0 {
		t.Fatalf("empty List() = %+v, %v", items, err)
	}
	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, ErrMerchantNotFound) {
		t.Fatalf("Get deleted error = %v", err)
	}
}

func TestMerchantRepositoryMaterialOrderAndCostCRUD(t *testing.T) {
	repo, merchant, projectID := merchantFixture(t)
	ctx := context.Background()
	material, err := repo.CreateMaterial(ctx, MerchantMaterial{MerchantID: merchant.ID, Kind: "video", Filename: "source.mp4", Path: "m/source.mp4", Size: 12, SHA256: "abc", Description: "正面", LicenseScope: "全平台", LicenseProof: "proof", ParentMaterialID: "parent", ProvenanceJSON: `{"camera":"a"}`})
	if err != nil {
		t.Fatal(err)
	}
	if material.ID == "" || material.LicenseSource != "merchant_shot" || material.CreatedAt.IsZero() {
		t.Fatalf("CreateMaterial() = %+v", material)
	}
	material.Description = "更新"
	material.Kind = "image"
	material.LicenseSource = "licensed_stock"
	material.LicenseExpiresAt = "2027-01-01"
	material.LicenseScope = "国内"
	updatedMaterial, err := repo.UpdateMaterial(ctx, material)
	if err != nil || updatedMaterial.Description != "更新" || updatedMaterial.Kind != "image" || updatedMaterial.Filename != "source.mp4" {
		t.Fatalf("UpdateMaterial() = %+v, %v", updatedMaterial, err)
	}
	materials, err := repo.ListMaterials(ctx, merchant.ID)
	if err != nil || len(materials) != 1 || materials[0].SHA256 != "abc" {
		t.Fatalf("ListMaterials() = %+v, %v", materials, err)
	}

	order, err := repo.CreateOrder(ctx, Order{MerchantID: merchant.ID, Title: "月度短视频", PriceCents: 100000, PaidCents: 60000, VideoQuota: 8, MaxDurationS: 90, RevisionRounds: 2, DueDate: "2026-11-01", ScopeNotes: "8条", RenewalOf: "old-order"})
	if err != nil {
		t.Fatal(err)
	}
	if order.ID == "" || order.Package != "custom" || order.Status != "draft" {
		t.Fatalf("CreateOrder() = %+v", order)
	}
	order.Package = "monthly"
	order.Status = "producing"
	order.Title = "更新订单"
	updatedOrder, err := repo.UpdateOrder(ctx, order)
	if err != nil || updatedOrder.Package != "monthly" || updatedOrder.Status != "producing" || updatedOrder.Title != "更新订单" {
		t.Fatalf("UpdateOrder() = %+v, %v", updatedOrder, err)
	}
	orders, err := repo.ListOrders(ctx, merchant.ID, "producing")
	if err != nil || len(orders) != 1 || orders[0].ID != order.ID {
		t.Fatalf("ListOrders() = %+v, %v", orders, err)
	}

	if err := repo.AssignProjectToOrder(ctx, projectID, order.ID); err != nil {
		t.Fatal(err)
	}
	cost, err := repo.CreateCost(ctx, OrderCost{OrderID: order.ID, ProjectID: projectID, Kind: "labor", Minutes: 45, AmountCents: 9000, Note: "剪辑"})
	if err != nil {
		t.Fatal(err)
	}
	if cost.ID == "" || cost.OccurredAt.IsZero() {
		t.Fatalf("CreateCost() = %+v", cost)
	}
	costs, err := repo.ListCosts(ctx, order.ID)
	if err != nil || len(costs) != 1 || costs[0].ID != cost.ID {
		t.Fatalf("ListCosts() = %+v, %v", costs, err)
	}
	if err := repo.DeleteCost(ctx, cost.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteMaterial(ctx, material.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteOrder(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetMaterial(ctx, material.ID); !errors.Is(err, ErrMaterialNotFound) {
		t.Fatalf("GetMaterial deleted error = %v", err)
	}
	if _, err := repo.GetOrder(ctx, order.ID); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("GetOrder deleted error = %v", err)
	}
	var linked string
	var delivered *time.Time
	if err := repo.db.QueryRow(`SELECT order_id,delivered_at FROM projects WHERE id=?`, projectID).Scan(&linked, &delivered); err != nil || linked != "" || delivered != nil {
		t.Fatalf("project link after order delete = %q, %v, %v", linked, delivered, err)
	}
}

func TestMerchantRepositoryRejectsInvalidDataBeforeSQL(t *testing.T) {
	repo, merchant, _ := merchantFixture(t)
	ctx := context.Background()
	tests := []struct {
		name string
		run  func() error
	}{
		{"merchant status", func() error { _, err := repo.Create(ctx, Merchant{Name: "x", Status: "bad"}); return err }},
		{"merchant update empty status", func() error { merchant.Status = ""; _, err := repo.Update(ctx, merchant); return err }},
		{"material kind", func() error {
			_, err := repo.CreateMaterial(ctx, MerchantMaterial{MerchantID: merchant.ID, Kind: "bad", Filename: "x", Path: "x"})
			return err
		}},
		{"material license source", func() error {
			_, err := repo.CreateMaterial(ctx, MerchantMaterial{MerchantID: merchant.ID, Kind: "video", LicenseSource: "bad", Filename: "x", Path: "x"})
			return err
		}},
		{"order package", func() error {
			_, err := repo.CreateOrder(ctx, Order{MerchantID: merchant.ID, Title: "x", Package: "bad"})
			return err
		}},
		{"order status", func() error {
			_, err := repo.CreateOrder(ctx, Order{MerchantID: merchant.ID, Title: "x", Status: "bad"})
			return err
		}},
		{"cost kind", func() error { _, err := repo.CreateCost(ctx, OrderCost{OrderID: "missing", Kind: "bad"}); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, ErrInvalidMerchantData) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestMerchantRepositorySummaryAndOverview(t *testing.T) {
	repo, merchant, projectID := merchantFixture(t)
	ctx := context.Background()
	order, err := repo.CreateOrder(ctx, Order{MerchantID: merchant.ID, Title: "首单", Package: "monthly", Status: "producing", PriceCents: 120000, PaidCents: 70000})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.CreateOrder(ctx, Order{MerchantID: merchant.ID, Title: "续费", Status: "signed", PriceCents: 80000, PaidCents: 30000, RenewalOf: order.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignProjectToOrder(ctx, projectID, order.ID); err != nil {
		t.Fatal(err)
	}
	deliveredAt := time.Date(2026, 10, 12, 8, 30, 0, 0, time.UTC)
	if err := repo.DeliverOrderProject(ctx, order.ID, projectID, deliveredAt); err != nil {
		t.Fatal(err)
	}
	for _, cost := range []OrderCost{
		{OrderID: order.ID, ProjectID: projectID, Kind: "labor", Minutes: 50, AmountCents: 10000},
		{OrderID: order.ID, Kind: "api", Minutes: 999, AmountCents: 5000},
		{OrderID: order.ID, Kind: "revision", Minutes: 20, AmountCents: 3000},
		{OrderID: second.ID, Kind: "other", AmountCents: 4000},
	} {
		if _, err := repo.CreateCost(ctx, cost); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := repo.OrderSummary(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := OrderSummary{OrderID: order.ID, PriceCents: 120000, PaidCents: 70000, CostCents: 18000, LaborMinutes: 50, RevisionCount: 1, ProjectCount: 1, DeliveredProjectCount: 1, ContributionCents: 52000}
	if summary != want {
		t.Fatalf("OrderSummary() = %+v, want %+v", summary, want)
	}
	overview, err := repo.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if overview.MerchantsByStatus["prospect"] != 1 || overview.OrdersByStatus["producing"] != 1 || overview.OrdersByStatus["signed"] != 1 || overview.PriceCents != 200000 || overview.PaidCents != 100000 || overview.CostCents != 22000 || overview.ContributionCents != 78000 || overview.RenewalCount != 1 {
		t.Fatalf("Overview() = %+v", overview)
	}
}

func TestMerchantRepositoryProtectsOrderProjectOwnership(t *testing.T) {
	repo, merchant, projectID := merchantFixture(t)
	ctx := context.Background()
	first, _ := repo.CreateOrder(ctx, Order{MerchantID: merchant.ID, Title: "first"})
	second, _ := repo.CreateOrder(ctx, Order{MerchantID: merchant.ID, Title: "second"})
	if err := repo.AssignProjectToOrder(ctx, projectID, first.ID); err != nil {
		t.Fatal(err)
	}
	projects, err := repo.ListOrderProjects(ctx, first.ID)
	if err != nil || len(projects) != 1 || !projects[0].UpdatedAt.After(time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("assignment did not update project timestamp: %+v, %v", projects, err)
	}
	deliveredAt := time.Now().UTC().Add(-time.Minute)
	if err := repo.DeliverOrderProject(ctx, first.ID, projectID, deliveredAt); err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignProjectToOrder(ctx, projectID, first.ID); err != nil {
		t.Fatal(err)
	}
	projects, err = repo.ListOrderProjects(ctx, first.ID)
	if err != nil || len(projects) != 1 || projects[0].DeliveredAt == nil || !projects[0].DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("repeated assignment lost delivery: %+v, %v", projects, err)
	}
	if _, err := repo.CreateCost(ctx, OrderCost{OrderID: second.ID, ProjectID: projectID, Kind: "labor"}); !errors.Is(err, ErrInvalidMerchantData) {
		t.Fatalf("CreateCost wrong order error = %v", err)
	}
	when := time.Now().UTC()
	if err := repo.DeliverOrderProject(ctx, second.ID, projectID, when); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("DeliverOrderProject wrong order error = %v", err)
	}
	if err := repo.UnassignOrderProject(ctx, second.ID, projectID); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("UnassignOrderProject wrong order error = %v", err)
	}
	projects, err = repo.ListOrderProjects(ctx, first.ID)
	if err != nil || len(projects) != 1 || projects[0].ID != projectID || projects[0].DeliveredAt == nil {
		t.Fatalf("ListOrderProjects() = %+v, %v", projects, err)
	}
	if err := repo.UnassignOrderProject(ctx, first.ID, projectID); err != nil {
		t.Fatal(err)
	}
}

func TestMerchantRepositoryUpdateOrderRejectsMissingMerchant(t *testing.T) {
	repo, merchant, _ := merchantFixture(t)
	order, err := repo.CreateOrder(context.Background(), Order{MerchantID: merchant.ID, Title: "order"})
	if err != nil {
		t.Fatal(err)
	}
	order.MerchantID = uuid.NewString()
	if _, err := repo.UpdateOrder(context.Background(), order); !errors.Is(err, ErrMerchantNotFound) {
		t.Fatalf("UpdateOrder missing merchant error = %v", err)
	}
	got, err := repo.GetOrder(context.Background(), order.ID)
	if err != nil || got.MerchantID != merchant.ID {
		t.Fatalf("failed update changed order: %+v, %v", got, err)
	}
}

func TestMerchantDeleteCascadesChildrenAndClearsProjectLinks(t *testing.T) {
	repo, merchant, projectID := merchantFixture(t)
	ctx := context.Background()
	material, _ := repo.CreateMaterial(ctx, MerchantMaterial{MerchantID: merchant.ID, Kind: "video", Filename: "x", Path: "x"})
	order, _ := repo.CreateOrder(ctx, Order{MerchantID: merchant.ID, Title: "x"})
	if err := repo.AssignProjectToOrder(ctx, projectID, order.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkProjectDelivered(ctx, projectID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	cost, _ := repo.CreateCost(ctx, OrderCost{OrderID: order.ID, ProjectID: projectID, Kind: "api"})
	if err := repo.Delete(ctx, merchant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetMaterial(ctx, material.ID); !errors.Is(err, ErrMaterialNotFound) {
		t.Fatalf("material after merchant delete error = %v", err)
	}
	if _, err := repo.GetOrder(ctx, order.ID); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("order after merchant delete error = %v", err)
	}
	if err := repo.DeleteCost(ctx, cost.ID); !errors.Is(err, ErrCostNotFound) {
		t.Fatalf("cost after merchant delete error = %v", err)
	}
	var linked string
	var delivered *time.Time
	if err := repo.db.QueryRow(`SELECT order_id,delivered_at FROM projects WHERE id=?`, projectID).Scan(&linked, &delivered); err != nil || linked != "" || delivered != nil {
		t.Fatalf("project link after merchant delete = %q, %v, %v", linked, delivered, err)
	}
}

func merchantFixture(t *testing.T) (*MerchantRepository, Merchant, string) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "merchants.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	accountID, projectID := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "账号", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'项目','mixing',?,?)`, projectID, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewMerchantRepository(db)
	merchant, err := repo.Create(context.Background(), Merchant{Name: "商户"})
	if err != nil {
		t.Fatal(err)
	}
	return repo, merchant, projectID
}
