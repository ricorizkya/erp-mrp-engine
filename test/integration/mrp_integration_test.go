//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"erp-mrp-engine/internal/domain"
)

// Scenario 1: Simple MTO — 1 demand, BOM 1 level -> 1 planned_production + 2 planned_purchase
func TestIntegration_Scenario1_SimpleMTO(t *testing.T) {
	fixture := setupTestFixture(t)
	if fixture == nil {
		return
	}
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	planFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	planTo := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	neededDate := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

	// Setup Data: Produk Kaos (var 101) -> BOM -> Kain (var 201) & Benang (var 202)
	_, err := fixture.Pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.product_variants (id, sku, lead_time_days, base_uom_id) VALUES
		(101, 'KAOS-POLOS-L', 3, 1),
		(201, 'KAIN-COTTON-COMBED', 7, 2),
		(202, 'BENANG-JAHIT-PUTIH', 5, 3);

		INSERT INTO %s.bom_headers (id, variant_id, is_active) VALUES (1, 101, TRUE);
		INSERT INTO %s.bom_versions (id, bom_header_id, output_quantity, output_uom_id, status) VALUES (1, 1, 1.0, 1, 'active');
		INSERT INTO %s.bom_operations (id, bom_version_id, duration_minutes) VALUES (1, 1, 480); -- 1 day
		INSERT INTO %s.bom_items (bom_version_id, variant_id, quantity, quantity_with_scrap, uom_id) VALUES
		(1, 201, 0.25, 0.25, 2),
		(1, 202, 1.0, 1.0, 3);

		INSERT INTO %s.mrp_demands (id, variant_id, quantity, needed_date, status, warehouse_id)
		VALUES (1, 101, 100.0, $1, 'open', 1);
	`, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema), neededDate)
	if err != nil {
		t.Fatalf("failed to seed test data: %v", err)
	}

	runID := createTestMrpRun(t, fixture.Pool, planFrom, planTo)

	job := &domain.MrpJob{
		SchemaVersion: domain.SupportedSchemaVersion,
		RunID:         runID,
		TenantSchema:  TestTenantSchema,
		PlanFrom:      planFrom,
		PlanTo:        planTo,
		TriggeredBy:   1,
	}

	if err := fixture.Engine.Run(ctx, job); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	// Verifikasi mrp_runs status = 'completed'
	var status string
	var totalProd, totalPurch int
	err = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT status, total_planned_production, total_planned_purchase FROM %s.mrp_runs WHERE id = $1", TestTenantSchema),
		runID,
	).Scan(&status, &totalProd, &totalPurch)
	if err != nil {
		t.Fatalf("failed to query mrp_runs: %v", err)
	}

	if status != "completed" {
		t.Errorf("expected run status 'completed', got '%s'", status)
	}
	if totalProd != 1 || totalPurch != 2 {
		t.Errorf("expected 1 production and 2 purchase orders, got %d prod and %d purch", totalProd, totalPurch)
	}

	// Verifikasi demand status terupdate menjadi 'planned'
	var demandStatus string
	err = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT status FROM %s.mrp_demands WHERE id = 1", TestTenantSchema),
	).Scan(&demandStatus)
	if err != nil {
		t.Fatalf("failed to query demand status: %v", err)
	}
	if demandStatus != "planned" {
		t.Errorf("expected demand status 'planned', got '%s'", demandStatus)
	}
}

// Scenario 2: Sufficient Stock -> 0 planned orders
func TestIntegration_Scenario2_SufficientStock(t *testing.T) {
	fixture := setupTestFixture(t)
	if fixture == nil {
		return
	}
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	planFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	planTo := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	neededDate := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)

	_, err := fixture.Pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.product_variants (id, sku, lead_time_days) VALUES (102, 'KEMEJA-M', 7);
		INSERT INTO %s.stock_summary (variant_id, warehouse_id, quantity_on_hand) VALUES (102, 1, 80.0);
		INSERT INTO %s.mrp_demands (id, variant_id, quantity, needed_date, status, warehouse_id)
		VALUES (2, 102, 50.0, $1, 'open', 1);
	`, TestTenantSchema, TestTenantSchema, TestTenantSchema), neededDate)
	if err != nil {
		t.Fatalf("failed to seed test data: %v", err)
	}

	runID := createTestMrpRun(t, fixture.Pool, planFrom, planTo)

	job := &domain.MrpJob{
		SchemaVersion: domain.SupportedSchemaVersion,
		RunID:         runID,
		TenantSchema:  TestTenantSchema,
		PlanFrom:      planFrom,
		PlanTo:        planTo,
		TriggeredBy:   1,
	}

	if err := fixture.Engine.Run(ctx, job); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	var totalProd, totalPurch int
	_ = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT total_planned_production, total_planned_purchase FROM %s.mrp_runs WHERE id = $1", TestTenantSchema),
		runID,
	).Scan(&totalProd, &totalPurch)

	if totalProd != 0 || totalPurch != 0 {
		t.Errorf("expected 0 planned orders for sufficient stock, got %d prod and %d purch", totalProd, totalPurch)
	}
}

// Scenario 3: Partial Stock -> Net quantity order
func TestIntegration_Scenario3_PartialStock(t *testing.T) {
	fixture := setupTestFixture(t)
	if fixture == nil {
		return
	}
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	planFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	planTo := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	neededDate := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)

	// Demand 100, on-hand 40 -> planned order 60
	_, err := fixture.Pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.product_variants (id, sku, lead_time_days) VALUES (103, 'RAW-BUTTON', 5);
		INSERT INTO %s.stock_summary (variant_id, warehouse_id, quantity_on_hand) VALUES (103, 1, 40.0);
		INSERT INTO %s.mrp_demands (id, variant_id, quantity, needed_date, status, warehouse_id)
		VALUES (3, 103, 100.0, $1, 'open', 1);
	`, TestTenantSchema, TestTenantSchema, TestTenantSchema), neededDate)
	if err != nil {
		t.Fatalf("failed to seed test data: %v", err)
	}

	runID := createTestMrpRun(t, fixture.Pool, planFrom, planTo)

	job := &domain.MrpJob{
		SchemaVersion: domain.SupportedSchemaVersion,
		RunID:         runID,
		TenantSchema:  TestTenantSchema,
		PlanFrom:      planFrom,
		PlanTo:        planTo,
		TriggeredBy:   1,
	}

	if err := fixture.Engine.Run(ctx, job); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	var qty float64
	var orderType string
	err = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT quantity, order_type FROM %s.planned_orders WHERE mrp_run_id = $1 AND variant_id = 103", TestTenantSchema),
		runID,
	).Scan(&qty, &orderType)
	if err != nil {
		t.Fatalf("failed to query planned order: %v", err)
	}

	if qty != 60.0 {
		t.Errorf("expected planned order quantity 60.0, got %f", qty)
	}
	if orderType != "purchase" {
		t.Errorf("expected order_type 'purchase', got '%s'", orderType)
	}
}

// Scenario 4: On-Order PO Coverage
func TestIntegration_Scenario4_OnOrderCoverage(t *testing.T) {
	fixture := setupTestFixture(t)
	if fixture == nil {
		return
	}
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	planFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	planTo := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	neededDate := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)

	// Demand 100, on-hand 20, on-order 50 -> planned order 30
	_, err := fixture.Pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.product_variants (id, sku, lead_time_days) VALUES (104, 'ZIPPER-METAL-50CM', 7);
		INSERT INTO %s.stock_summary (variant_id, warehouse_id, quantity_on_hand) VALUES (104, 1, 20.0);
		INSERT INTO %s.purchase_orders (id, supplier_id, warehouse_id, status) VALUES (1, 10, 1, 'confirmed');
		INSERT INTO %s.purchase_order_items (id, po_id, variant_id, quantity_pending) VALUES (1, 1, 104, 50.0);
		INSERT INTO %s.mrp_demands (id, variant_id, quantity, needed_date, status, warehouse_id)
		VALUES (4, 104, 100.0, $1, 'open', 1);
	`, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema), neededDate)
	if err != nil {
		t.Fatalf("failed to seed test data: %v", err)
	}

	runID := createTestMrpRun(t, fixture.Pool, planFrom, planTo)

	job := &domain.MrpJob{
		SchemaVersion: domain.SupportedSchemaVersion,
		RunID:         runID,
		TenantSchema:  TestTenantSchema,
		PlanFrom:      planFrom,
		PlanTo:        planTo,
		TriggeredBy:   1,
	}

	if err := fixture.Engine.Run(ctx, job); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	var qty float64
	err = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT quantity FROM %s.planned_orders WHERE mrp_run_id = $1 AND variant_id = 104", TestTenantSchema),
		runID,
	).Scan(&qty)
	if err != nil {
		t.Fatalf("failed to query planned order: %v", err)
	}

	if qty != 30.0 {
		t.Errorf("expected planned order quantity 30.0 (100 - 20 - 50), got %f", qty)
	}
}

// Scenario 5: Multiple Demands Same Variant -> Aggregated single planned order
func TestIntegration_Scenario5_DemandAggregation(t *testing.T) {
	fixture := setupTestFixture(t)
	if fixture == nil {
		return
	}
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	planFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	planTo := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)

	// 3 separate SO demands for same variant
	_, err := fixture.Pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.product_variants (id, sku, lead_time_days) VALUES (105, 'POLO-SHIRT-NAVY-M', 7);
		INSERT INTO %s.mrp_demands (id, variant_id, quantity, needed_date, status, warehouse_id) VALUES
		(11, 105, 25.0, '2026-09-10', 'open', 1),
		(12, 105, 35.0, '2026-09-15', 'open', 1),
		(13, 105, 40.0, '2026-09-08', 'open', 1);
	`, TestTenantSchema, TestTenantSchema))
	if err != nil {
		t.Fatalf("failed to seed test data: %v", err)
	}

	runID := createTestMrpRun(t, fixture.Pool, planFrom, planTo)

	job := &domain.MrpJob{
		SchemaVersion: domain.SupportedSchemaVersion,
		RunID:         runID,
		TenantSchema:  TestTenantSchema,
		PlanFrom:      planFrom,
		PlanTo:        planTo,
		TriggeredBy:   1,
	}

	if err := fixture.Engine.Run(ctx, job); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	// Harus hanya ada 1 planned order agregasi dengan qty = 100.0
	var count int
	var qty float64
	var orderID int64
	err = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT id, quantity FROM %s.planned_orders WHERE mrp_run_id = $1 AND variant_id = 105", TestTenantSchema),
		runID,
	).Scan(&orderID, &qty)
	if err != nil {
		t.Fatalf("failed to query planned order: %v", err)
	}

	if qty != 100.0 {
		t.Errorf("expected aggregated quantity 100.0, got %f", qty)
	}

	// Cek planned_order_demands mapping
	err = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s.planned_order_demands WHERE planned_order_id = $1", TestTenantSchema),
		orderID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query planned_order_demands: %v", err)
	}

	if count != 3 {
		t.Errorf("expected 3 linked demand mappings, got %d", count)
	}
}

// Scenario 6: Phantom Component Handling
func TestIntegration_Scenario6_PhantomComponent(t *testing.T) {
	fixture := setupTestFixture(t)
	if fixture == nil {
		return
	}
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	planFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	planTo := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	neededDate := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

	// Produk 106 -> BOM -> Phantom Component (Var 500) -> Sub-BOM -> Raw 601 & Raw 602
	_, err := fixture.Pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.product_variants (id, sku, lead_time_days) VALUES
		(106, 'JAKET-WINTER', 5),
		(500, 'PHANTOM-SUBASSEMBLY', 0),
		(601, 'KAIN-WATERPROOF', 7),
		(602, 'BUSA-INSULASI', 5);

		-- BOM Main (Jaket)
		INSERT INTO %s.bom_headers (id, variant_id, is_active) VALUES (10, 106, TRUE);
		INSERT INTO %s.bom_versions (id, bom_header_id, output_quantity, status) VALUES (10, 10, 1.0, 'active');
		INSERT INTO %s.bom_items (bom_version_id, variant_id, is_phantom, quantity, quantity_with_scrap) VALUES
		(10, 500, TRUE, 1.0, 1.0);

		-- BOM Phantom (Subassembly)
		INSERT INTO %s.bom_headers (id, variant_id, is_active) VALUES (11, 500, TRUE);
		INSERT INTO %s.bom_versions (id, bom_header_id, output_quantity, status) VALUES (11, 11, 1.0, 'active');
		INSERT INTO %s.bom_items (bom_version_id, variant_id, is_phantom, quantity, quantity_with_scrap) VALUES
		(11, 601, FALSE, 2.0, 2.0),
		(11, 602, FALSE, 1.5, 1.5);

		INSERT INTO %s.mrp_demands (id, variant_id, quantity, needed_date, status, warehouse_id)
		VALUES (6, 106, 10.0, $1, 'open', 1);
	`, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema), neededDate)
	if err != nil {
		t.Fatalf("failed to seed test data: %v", err)
	}

	runID := createTestMrpRun(t, fixture.Pool, planFrom, planTo)

	job := &domain.MrpJob{
		SchemaVersion: domain.SupportedSchemaVersion,
		RunID:         runID,
		TenantSchema:  TestTenantSchema,
		PlanFrom:      planFrom,
		PlanTo:        planTo,
		TriggeredBy:   1,
	}

	if err := fixture.Engine.Run(ctx, job); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	// Verifikasi: Var 500 (Phantom) TIDAK BOLEH dibuatkan planned order.
	// Yang dibuat: 1 production (Jaket 106) dan 2 purchase (Raw 601 qty 20 & Raw 602 qty 15).
	var phantomCount int
	err = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s.planned_orders WHERE mrp_run_id = $1 AND variant_id = 500", TestTenantSchema),
		runID,
	).Scan(&phantomCount)
	if err != nil {
		t.Fatalf("failed to query phantom count: %v", err)
	}

	if phantomCount != 0 {
		t.Errorf("expected 0 planned orders for phantom variant 500, got %d", phantomCount)
	}

	var totalOrders int
	err = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s.planned_orders WHERE mrp_run_id = $1", TestTenantSchema),
		runID,
	).Scan(&totalOrders)
	if err != nil {
		t.Fatalf("failed to query total planned orders: %v", err)
	}

	if totalOrders != 3 {
		t.Errorf("expected 3 planned orders (1 prod + 2 raw purchase), got %d", totalOrders)
	}
}

// Scenario 7: Overdue Demand Handling
func TestIntegration_Scenario7_OverdueDemand(t *testing.T) {
	fixture := setupTestFixture(t)
	if fixture == nil {
		return
	}
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	planFrom := today
	planTo := today.AddDate(0, 1, 0)

	// Needed date 2 hari lagi tapi lead time pembelian butuh 14 hari -> planned_start disesuaikan ke hari ini dan is_overdue=true
	neededDate := today.AddDate(0, 0, 2)

	_, err := fixture.Pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.product_variants (id, sku, lead_time_days) VALUES (107, 'IMPORT-FABRIC-SILK', 14);
		INSERT INTO %s.mrp_demands (id, variant_id, quantity, needed_date, status, warehouse_id)
		VALUES (7, 107, 50.0, $1, 'open', 1);
	`, TestTenantSchema, TestTenantSchema), neededDate)
	if err != nil {
		t.Fatalf("failed to seed test data: %v", err)
	}

	runID := createTestMrpRun(t, fixture.Pool, planFrom, planTo)

	job := &domain.MrpJob{
		SchemaVersion: domain.SupportedSchemaVersion,
		RunID:         runID,
		TenantSchema:  TestTenantSchema,
		PlanFrom:      planFrom,
		PlanTo:        planTo,
		TriggeredBy:   1,
	}

	if err := fixture.Engine.Run(ctx, job); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	var plannedStart time.Time
	err = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT planned_start FROM %s.planned_orders WHERE mrp_run_id = $1 AND variant_id = 107", TestTenantSchema),
		runID,
	).Scan(&plannedStart)
	if err != nil {
		t.Fatalf("failed to query planned order: %v", err)
	}

	if plannedStart.UTC().Truncate(24 * time.Hour).Before(today) {
		t.Errorf("expected planned_start not to be before today (%v), got %v", today, plannedStart)
	}
}

// Scenario 8: Multi-level BOM Cascading (3 Level)
func TestIntegration_Scenario8_MultiLevelBomCascading(t *testing.T) {
	fixture := setupTestFixture(t)
	if fixture == nil {
		return
	}
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	planFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	planTo := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	neededDate := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)

	// Level 1: Kaos (Var 801, Ver 81)
	// Level 2: Kain Jadi (Var 802, Ver 82)
	// Level 3: Kapas (Var 803, Raw Material)
	_, err := fixture.Pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.product_variants (id, sku, lead_time_days) VALUES
		(801, 'KAOS-PREMIUM', 3),
		(802, 'KAIN-COMBED-30S', 4),
		(803, 'KAPAS-ORGANIK', 7);

		INSERT INTO %s.bom_headers (id, variant_id, is_active) VALUES
		(81, 801, TRUE),
		(82, 802, TRUE);

		INSERT INTO %s.bom_versions (id, bom_header_id, output_quantity, status) VALUES
		(81, 81, 1.0, 'active'),
		(82, 82, 1.0, 'active');

		INSERT INTO %s.bom_items (bom_version_id, variant_id, is_phantom, quantity, quantity_with_scrap) VALUES
		(81, 802, FALSE, 0.3, 0.3),
		(82, 803, FALSE, 1.1, 1.1);

		INSERT INTO %s.mrp_demands (id, variant_id, quantity, needed_date, status, warehouse_id)
		VALUES (8, 801, 100.0, $1, 'open', 1);
	`, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema, TestTenantSchema), neededDate)
	if err != nil {
		t.Fatalf("failed to seed test data: %v", err)
	}

	runID := createTestMrpRun(t, fixture.Pool, planFrom, planTo)

	job := &domain.MrpJob{
		SchemaVersion: domain.SupportedSchemaVersion,
		RunID:         runID,
		TenantSchema:  TestTenantSchema,
		PlanFrom:      planFrom,
		PlanTo:        planTo,
		TriggeredBy:   1,
	}

	if err := fixture.Engine.Run(ctx, job); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	// Verifikasi ada 3 planned orders:
	// 1. Production Kaos (qty 100)
	// 2. Production Kain (qty 30)
	// 3. Purchase Kapas (qty 33)
	var totalProd, totalPurch int
	err = fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT total_planned_production, total_planned_purchase FROM %s.mrp_runs WHERE id = $1", TestTenantSchema),
		runID,
	).Scan(&totalProd, &totalPurch)
	if err != nil {
		t.Fatalf("failed to query mrp_runs: %v", err)
	}

	if totalProd != 2 || totalPurch != 1 {
		t.Errorf("expected 2 production orders and 1 purchase order, got %d prod and %d purch", totalProd, totalPurch)
	}
}
