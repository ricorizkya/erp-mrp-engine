//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-mrp-engine/internal/domain"
	infradb "erp-mrp-engine/internal/infra/db"
)

func TestStateMachine_ClaimIsVisibleAndCannotBeClaimedTwice(t *testing.T) {
	fixture := setupTestFixture(t)
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	runID := createTestMrpRun(t, fixture.Pool, time.Now(), time.Now().AddDate(0, 0, 7))
	var acquired bool
	if err := infradb.WithTenantSchema(ctx, fixture.Pool, TestTenantSchema, func(tx pgx.Tx) error {
		var err error
		acquired, err = infradb.MarkRunning(ctx, tx, runID, domain.SupportedSchemaVersion)
		return err
	}); err != nil {
		t.Fatalf("claim run: %v", err)
	}
	if !acquired {
		t.Fatal("first worker did not acquire pending run")
	}

	var status string
	if err := fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT status FROM %s.mrp_runs WHERE id = $1", TestTenantSchema),
		runID,
	).Scan(&status); err != nil {
		t.Fatalf("read claimed run: %v", err)
	}
	if status != "running" {
		t.Fatalf("claim was not visible from another connection: %s", status)
	}

	if err := infradb.WithTenantSchema(ctx, fixture.Pool, TestTenantSchema, func(tx pgx.Tx) error {
		claimedAgain, err := infradb.MarkRunning(ctx, tx, runID, domain.SupportedSchemaVersion)
		if claimedAgain {
			t.Fatal("second worker acquired an already running run")
		}
		return err
	}); err != nil {
		t.Fatalf("second claim: %v", err)
	}
}

func TestStateMachine_MarkFailedDoesNotOverwriteCancelled(t *testing.T) {
	fixture := setupTestFixture(t)
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	runID := createTestMrpRun(t, fixture.Pool, time.Now(), time.Now().AddDate(0, 0, 7))
	if _, err := fixture.Pool.Exec(ctx,
		fmt.Sprintf("UPDATE %s.mrp_runs SET status = 'cancelled' WHERE id = $1", TestTenantSchema),
		runID,
	); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	if err := infradb.WithTenantSchema(ctx, fixture.Pool, TestTenantSchema, func(tx pgx.Tx) error {
		return infradb.MarkFailed(ctx, tx, runID, "late worker error")
	}); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	var status string
	if err := fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT status FROM %s.mrp_runs WHERE id = $1", TestTenantSchema),
		runID,
	).Scan(&status); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("late failure overwrote cancellation: %s", status)
	}
}

func TestStateMachine_CancelledWriterPreservesExistingResults(t *testing.T) {
	fixture := setupTestFixture(t)
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	runID := createTestMrpRun(t, fixture.Pool, time.Now(), time.Now().AddDate(0, 0, 7))
	_, err := fixture.Pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.mrp_runs SET status = 'cancelled' WHERE id = $1;
		INSERT INTO %s.planned_orders
			(mrp_run_id, variant_id, order_type, quantity, uom_id, planned_start, planned_finish)
		VALUES ($1, 10, 'purchase', 5, 1, CURRENT_DATE, CURRENT_DATE + 1)
	`, TestTenantSchema, TestTenantSchema), runID)
	if err != nil {
		t.Fatalf("seed cancelled run: %v", err)
	}

	err = infradb.WithTenantSchema(ctx, fixture.Pool, TestTenantSchema, func(tx pgx.Tx) error {
		return infradb.WriteResults(ctx, tx, runID, &domain.CalcResult{}, 1)
	})
	if err == nil {
		t.Fatal("writer accepted a cancelled run")
	}

	var count int
	if err := fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s.planned_orders WHERE mrp_run_id = $1", TestTenantSchema),
		runID,
	).Scan(&count); err != nil {
		t.Fatalf("count planned orders: %v", err)
	}
	if count != 1 {
		t.Fatalf("cancelled writer changed existing results: %d", count)
	}
}

func TestStateMachine_RetryWriteIsIdempotent(t *testing.T) {
	fixture := setupTestFixture(t)
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	runID := createTestMrpRun(t, fixture.Pool, time.Now(), time.Now().AddDate(0, 0, 7))
	result := &domain.CalcResult{PlannedOrders: []domain.PlannedOrder{{
		MrpRunID:      runID,
		VariantID:     10,
		WarehouseID:   1,
		OrderType:     domain.OrderTypePurchase,
		Quantity:      5,
		UomID:         1,
		PlannedStart:  time.Now(),
		PlannedFinish: time.Now().AddDate(0, 0, 1),
	}}}

	write := func() error {
		return infradb.WithTenantSchema(ctx, fixture.Pool, TestTenantSchema, func(tx pgx.Tx) error {
			return infradb.WriteResults(ctx, tx, runID, result, 1)
		})
	}
	if _, err := fixture.Pool.Exec(ctx,
		fmt.Sprintf("UPDATE %s.mrp_runs SET status = 'running' WHERE id = $1", TestTenantSchema),
		runID,
	); err != nil {
		t.Fatalf("prepare first write: %v", err)
	}
	if err := write(); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := fixture.Pool.Exec(ctx,
		fmt.Sprintf("UPDATE %s.mrp_runs SET status = 'running' WHERE id = $1", TestTenantSchema),
		runID,
	); err != nil {
		t.Fatalf("prepare retry: %v", err)
	}
	if err := write(); err != nil {
		t.Fatalf("retry write: %v", err)
	}

	var count int
	if err := fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s.planned_orders WHERE mrp_run_id = $1", TestTenantSchema),
		runID,
	).Scan(&count); err != nil {
		t.Fatalf("count planned orders: %v", err)
	}
	if count != 1 {
		t.Fatalf("retry duplicated planned orders: %d", count)
	}
}

func TestStateMachine_SchemaMismatchIsPermanent(t *testing.T) {
	fixture := setupTestFixture(t)
	defer fixture.Pool.Close()
	cleanTestTables(t, fixture.Pool)

	ctx := context.Background()
	runID := createTestMrpRun(t, fixture.Pool, time.Now(), time.Now().AddDate(0, 0, 7))
	err := fixture.Engine.Run(ctx, &domain.MrpJob{
		SchemaVersion: domain.SupportedSchemaVersion + 1,
		RunID:         runID,
		TenantSchema:  TestTenantSchema,
		PlanFrom:      time.Now(),
		PlanTo:        time.Now().AddDate(0, 0, 7),
		TriggeredBy:   1,
	})
	if err == nil {
		t.Fatal("engine accepted an unsupported schema version")
	}

	var status string
	if err := fixture.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT status FROM %s.mrp_runs WHERE id = $1", TestTenantSchema),
		runID,
	).Scan(&status); err != nil {
		t.Fatalf("read failed run: %v", err)
	}
	if status != "failed_permanent" {
		t.Fatalf("schema mismatch was not permanent: %s", status)
	}
}
