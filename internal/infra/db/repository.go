package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"erp-mrp-engine/internal/domain"
	"erp-mrp-engine/internal/infra/db/queries"
)

// MarkRunning menandai mrp_runs sebagai 'running' dan mencatat started_at serta job_schema_version.
// Pengecekan WHERE status = 'pending' berfungsi sebagai perlindungan terhadap race condition antar worker.
// Mengembalikan true jika baris berhasil diupdate, atau false jika status bukan 'pending' lagi.
func MarkRunning(ctx context.Context, tx pgx.Tx, runID int64, schemaVersion int) (bool, error) {
	tag, err := tx.Exec(ctx,
		`UPDATE mrp_runs
		 SET status = 'running', started_at = NOW(), job_schema_version = $2
		 WHERE id = $1 AND status = 'pending'`,
		runID, schemaVersion,
	)
	if err != nil {
		return false, fmt.Errorf("mark running: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkFailed mencatat kegagalan transien pada mrp_runs sehingga dapat direkonsiliasi dan diretry oleh NestJS.
func MarkFailed(ctx context.Context, tx pgx.Tx, runID int64, reason string) error {
	_, err := tx.Exec(ctx,
		`UPDATE mrp_runs
		 SET status = 'failed', error_message = $2, completed_at = NOW()
		 WHERE id = $1`,
		runID, reason,
	)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	return nil
}

// MarkFailedPermanent mencatat kegagalan permanen (seperti schemaVersion mismatch) yang tidak boleh diretry.
func MarkFailedPermanent(ctx context.Context, tx pgx.Tx, runID int64, reason string) error {
	_, err := tx.Exec(ctx,
		`UPDATE mrp_runs
		 SET status = 'failed_permanent', error_message = $2, completed_at = NOW()
		 WHERE id = $1`,
		runID, reason,
	)
	if err != nil {
		return fmt.Errorf("mark failed_permanent: %w", err)
	}
	return nil
}

// WriteResults menyimpan seluruh hasil kalkulasi MRP ke database dalam satu transaksi:
// 1. Bulk insert planned_orders dan mendapatkan daftar ID hasil insert
// 2. Memetakan ID planned_orders ke planned_order_demands lalu bulk insert
// 3. Bulk update status mrp_demands menjadi 'planned'
// 4. Update status mrp_runs menjadi 'completed' beserta ringkasan statistik dan durasi
func WriteResults(ctx context.Context, tx pgx.Tx, runID int64, result *domain.CalcResult, durationMs int64) error {
	if result == nil {
		return fmt.Errorf("cannot write nil calc result for run %d", runID)
	}

	// 1. Bulk insert planned_orders
	orderIDs, err := queries.BulkInsertPlannedOrders(ctx, tx, result.PlannedOrders)
	if err != nil {
		return fmt.Errorf("bulk insert planned orders: %w", err)
	}

	// 2. Hubungkan ID yang dihasilkan ke relasi planned_order_demands
	var allDemandsLinks []domain.PlannedOrderDemand
	for i, order := range result.PlannedOrders {
		if i < len(orderIDs) {
			assignedID := orderIDs[i]
			for _, demandLink := range order.CoveredDemands {
				demandLink.PlannedOrderID = assignedID
				allDemandsLinks = append(allDemandsLinks, demandLink)
			}
		}
	}

	if len(allDemandsLinks) > 0 {
		if err := queries.BulkInsertPlannedOrderDemands(ctx, tx, allDemandsLinks); err != nil {
			return fmt.Errorf("bulk insert planned order demands: %w", err)
		}
	}

	// 3. Update status demand yang ter-cover
	if len(result.PlannedDemandIDs) > 0 {
		if err := queries.BulkUpdateDemandStatus(ctx, tx, result.PlannedDemandIDs, "planned"); err != nil {
			return fmt.Errorf("bulk update demand status: %w", err)
		}
	}

	// 4. Update mrp_runs menjadi 'completed'
	_, err = tx.Exec(ctx,
		`UPDATE mrp_runs
		 SET status = 'completed',
		     completed_at = NOW(),
		     total_planned_production = $2,
		     total_planned_purchase = $3,
		     duration_ms = $4
		 WHERE id = $1`,
		runID,
		result.Stats.TotalProduction,
		result.Stats.TotalPurchase,
		durationMs,
	)
	if err != nil {
		return fmt.Errorf("update mrp_runs completed: %w", err)
	}

	return nil
}

// CompleteRun adalah helper minimal untuk menandai mrp_runs selesai (berguna saat test pipeline stub).
func CompleteRun(ctx context.Context, tx pgx.Tx, runID int64, totalProduction, totalPurchase int, durationMs int64) error {
	_, err := tx.Exec(ctx,
		`UPDATE mrp_runs
		 SET status = 'completed',
		     completed_at = NOW(),
		     total_planned_production = $2,
		     total_planned_purchase = $3,
		     duration_ms = $4
		 WHERE id = $1`,
		runID, totalProduction, totalPurchase, durationMs,
	)
	if err != nil {
		return fmt.Errorf("complete run: %w", err)
	}
	return nil
}
