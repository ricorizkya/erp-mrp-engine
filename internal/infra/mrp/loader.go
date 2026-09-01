// Package mrp mengorkestrasikan alur end-to-end MRP calculation (Load -> Calculate -> Write).
package mrp

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"erp-mrp-engine/internal/domain"
	"erp-mrp-engine/internal/infra/db/queries"
	"erp-mrp-engine/internal/logger"
)

// Loader bertugas membaca seluruh data input MRP dari PostgreSQL ke dalam struktur domain.InputData.
type Loader struct {
	pool   *pgxpool.Pool
	logger *logger.Logger
}

// NewLoader membuat instance Loader baru.
func NewLoader(pool *pgxpool.Pool, log *logger.Logger) *Loader {
	return &Loader{
		pool:   pool,
		logger: log,
	}
}

// LoadAll memuat seluruh data input dalam planning horizon yang ditentukan.
func (l *Loader) LoadAll(
	ctx context.Context,
	tx pgx.Tx,
	runID int64,
	tenantSchema string,
	planFrom, planTo time.Time,
) (*domain.InputData, error) {
	input := domain.NewInputData(runID, tenantSchema, planFrom, planTo)

	// 1. Load open demands
	demands, err := queries.LoadOpenDemands(ctx, tx, planFrom, planTo)
	if err != nil {
		return nil, fmt.Errorf("load open demands: %w", err)
	}
	input.Demands = demands

	// Ekstrak daftar variant_id unik dari demands
	variantMap := make(map[int64]struct{})
	for _, d := range demands {
		variantMap[d.VariantID] = struct{}{}
	}
	variantIDs := make([]int64, 0, len(variantMap))
	for id := range variantMap {
		variantIDs = append(variantIDs, id)
	}

	// 2. Load stock summary
	stockMap, err := queries.LoadStockSummary(ctx, tx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("load stock summary: %w", err)
	}
	input.StockMap = stockMap

	// 3. Load on-order quantities dari purchase orders
	onOrderMap, err := queries.LoadOnOrder(ctx, tx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("load on-order: %w", err)
	}
	input.OnOrderMap = onOrderMap

	// 4. Load semua BOM aktif
	activeBoms, outputQty, leadTime, err := queries.LoadActiveBoms(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("load active boms: %w", err)
	}
	input.ActiveBoms = activeBoms
	input.BomOutputQty = outputQty
	input.BomLeadTime = leadTime

	// 5. Load semua bom_items
	bomItems, err := queries.LoadAllBomItems(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("load bom items: %w", err)
	}
	input.BomItems = bomItems

	// 6. Load metadata variant
	variantInfo, err := queries.LoadVariantInfo(ctx, tx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("load variant info: %w", err)
	}
	input.VariantInfo = variantInfo

	// 7. Load riwayat supplier
	supplierHistory, err := queries.LoadSupplierHistory(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("load supplier history: %w", err)
	}
	input.SupplierHistory = supplierHistory

	l.logger.WithJobContext(runID, tenantSchema).InfoContext(ctx,
		"input data loaded successfully",
		"total_demands", len(demands),
		"unique_variants", len(variantIDs),
		"active_boms", len(activeBoms),
	)

	return input, nil
}
