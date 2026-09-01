// Package queries menyediakan fungsi eksekusi SQL mentah untuk membaca dan menulis data MRP.
package queries

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-mrp-engine/internal/domain"
)

// LoadOpenDemands memuat seluruh mrp_demands dengan status 'open' atau 'planned' dalam rentang planning horizon.
func LoadOpenDemands(ctx context.Context, tx pgx.Tx, planFrom, planTo time.Time) ([]domain.MrpDemand, error) {
	rows, err := tx.Query(ctx,
		`SELECT
			id,
			variant_id,
			demand_type,
			quantity,
			uom_id,
			needed_date,
			warehouse_id,
			so_id,
			so_item_id,
			status
		 FROM mrp_demands
		 WHERE status IN ('open', 'planned')
		   AND needed_date BETWEEN $1 AND $2
		 ORDER BY needed_date ASC`,
		planFrom, planTo,
	)
	if err != nil {
		return nil, fmt.Errorf("load open demands query: %w", err)
	}
	defer rows.Close()

	demands := make([]domain.MrpDemand, 0)
	for rows.Next() {
		var d domain.MrpDemand
		if err := rows.Scan(
			&d.ID,
			&d.VariantID,
			&d.DemandType,
			&d.Quantity,
			&d.UomID,
			&d.NeededDate,
			&d.WarehouseID,
			&d.SoID,
			&d.SoItemID,
			&d.Status,
		); err != nil {
			return nil, fmt.Errorf("scan demand row: %w", err)
		}
		demands = append(demands, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate demands rows: %w", err)
	}

	return demands, nil
}

// LoadVariantInfo memuat metadata produk seperti lead_time_days dan base_uom_id.
func LoadVariantInfo(ctx context.Context, tx pgx.Tx, variantIDs []int64) (map[int64]domain.VariantInfo, error) {
	result := make(map[int64]domain.VariantInfo)
	if len(variantIDs) == 0 {
		return result, nil
	}

	rows, err := tx.Query(ctx,
		`SELECT
			id,
			COALESCE(lead_time_days, 7) AS lead_time_days,
			base_uom_id
		 FROM product_variants
		 WHERE id = ANY($1::bigint[])`,
		variantIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("load variant info query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var info domain.VariantInfo
		if err := rows.Scan(&info.VariantID, &info.LeadTimeDays, &info.BaseUomID); err != nil {
			return nil, fmt.Errorf("scan variant info row: %w", err)
		}
		result[info.VariantID] = info
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate variant info rows: %w", err)
	}

	return result, nil
}
