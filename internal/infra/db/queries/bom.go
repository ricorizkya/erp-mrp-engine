package queries

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"erp-mrp-engine/internal/domain"
)

// ActiveBomHeader menyimpan relasi BOM aktif, output quantity, dan estimasi lead time operasi.
type ActiveBomHeader struct {
	VariantID      int64
	BomVersionID   int64
	OutputQuantity float64
	OutputUomID    int64
	LeadTimeDays   int
}

// LoadActiveBoms memuat seluruh versi BOM yang aktif di tenant saat ini.
// Memuat semua BOM aktif sekaligus untuk mendukung recursive explosion hingga komponen terdalam.
func LoadActiveBoms(ctx context.Context, tx pgx.Tx) (
	activeMap map[int64]int64,
	outputQty map[int64]float64,
	leadTime map[int64]int,
	err error,
) {
	activeMap = make(map[int64]int64)
	outputQty = make(map[int64]float64)
	leadTime = make(map[int64]int)

	rows, err := tx.Query(ctx,
		`SELECT
			bh.variant_id,
			bv.id AS bom_version_id,
			bv.output_quantity,
			bv.output_uom_id,
			COALESCE(
				(SELECT CEIL(SUM(duration_minutes) / 480.0)
				 FROM bom_operations
				 WHERE bom_version_id = bv.id),
				1
			)::INTEGER AS lead_time_days
		 FROM bom_headers bh
		 JOIN bom_versions bv ON bv.bom_header_id = bh.id
		 WHERE bv.status = 'active'
		   AND bh.is_active = true`,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load active boms query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var header ActiveBomHeader
		if err := rows.Scan(
			&header.VariantID,
			&header.BomVersionID,
			&header.OutputQuantity,
			&header.OutputUomID,
			&header.LeadTimeDays,
		); err != nil {
			return nil, nil, nil, fmt.Errorf("scan active bom row: %w", err)
		}

		activeMap[header.VariantID] = header.BomVersionID
		outputQty[header.BomVersionID] = header.OutputQuantity
		leadTime[header.BomVersionID] = header.LeadTimeDays
	}

	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("iterate active boms rows: %w", err)
	}

	return activeMap, outputQty, leadTime, nil
}

// LoadAllBomItems memuat semua baris rincian komponen (bom_items) untuk seluruh BOM aktif.
// Mengembalikan map berstruktur map[bomVersionID][]BomItem.
func LoadAllBomItems(ctx context.Context, tx pgx.Tx) (map[int64][]domain.BomItem, error) {
	bomItemsMap := make(map[int64][]domain.BomItem)

	rows, err := tx.Query(ctx,
		`SELECT
			id,
			bom_version_id,
			parent_item_id,
			variant_id,
			is_phantom,
			quantity,
			uom_id,
			scrap_pct,
			quantity_with_scrap,
			level,
			sequence
		 FROM bom_items
		 WHERE bom_version_id IN (
			SELECT id FROM bom_versions WHERE status = 'active'
		 )
		 ORDER BY bom_version_id, level, sequence`,
	)
	if err != nil {
		return nil, fmt.Errorf("load all bom items query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var item domain.BomItem
		if err := rows.Scan(
			&item.ID,
			&item.BomVersionID,
			&item.ParentItemID,
			&item.VariantID,
			&item.IsPhantom,
			&item.Quantity,
			&item.UomID,
			&item.ScrapPct,
			&item.QuantityWithScrap,
			&item.Level,
			&item.Sequence,
		); err != nil {
			return nil, fmt.Errorf("scan bom item row: %w", err)
		}

		bomItemsMap[item.BomVersionID] = append(bomItemsMap[item.BomVersionID], item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bom items rows: %w", err)
	}

	return bomItemsMap, nil
}
