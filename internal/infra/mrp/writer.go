package mrp

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"erp-mrp-engine/internal/domain"
	"erp-mrp-engine/internal/infra/db"
	"erp-mrp-engine/internal/logger"
)

// Writer bertugas menuliskan seluruh hasil kalkulasi MRP ke database secara atomik.
type Writer struct {
	pool   *pgxpool.Pool
	logger *logger.Logger
}

// NewWriter membuat instance Writer baru.
func NewWriter(pool *pgxpool.Pool, log *logger.Logger) *Writer {
	return &Writer{
		pool:   pool,
		logger: log,
	}
}

// Write mengeksekusi penulisan hasil kalkulasi MRP ke database via db.WriteResults.
func (w *Writer) Write(
	ctx context.Context,
	tx pgx.Tx,
	runID int64,
	tenantSchema string,
	result *domain.CalcResult,
	durationMs int64,
) error {
	if err := db.WriteResults(ctx, tx, runID, result, durationMs); err != nil {
		return fmt.Errorf("write results to database: %w", err)
	}

	w.logger.WithJobContext(runID, tenantSchema).InfoContext(ctx,
		"mrp results saved to database",
		"total_planned_orders", len(result.PlannedOrders),
		"production_orders", result.Stats.TotalProduction,
		"purchase_orders", result.Stats.TotalPurchase,
		"fulfilled_demands", result.Stats.TotalFulfilled,
		"overdue_orders", result.Stats.OverdueOrders,
		"duration_ms", durationMs,
	)

	return nil
}
