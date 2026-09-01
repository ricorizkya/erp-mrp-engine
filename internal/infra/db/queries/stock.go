package queries

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"erp-mrp-engine/internal/domain"
)

// LoadStockSummary memuat saldo stok (quantity_on_hand) dari materialized view stock_summary.
// Mengembalikan map berstruktur map[variantID][warehouseID] = quantityOnHand.
func LoadStockSummary(ctx context.Context, tx pgx.Tx, variantIDs []int64) (map[int64]map[int64]float64, error) {
	stockMap := make(map[int64]map[int64]float64)
	if len(variantIDs) == 0 {
		return stockMap, nil
	}

	rows, err := tx.Query(ctx,
		`SELECT
			variant_id,
			warehouse_id,
			SUM(quantity_on_hand) AS total_on_hand
		 FROM stock_summary
		 WHERE quantity_on_hand > 0
		   AND variant_id = ANY($1::bigint[])
		 GROUP BY variant_id, warehouse_id`,
		variantIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("load stock summary query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var variantID int64
		var warehouseID int64
		var totalOnHand float64

		if err := rows.Scan(&variantID, &warehouseID, &totalOnHand); err != nil {
			return nil, fmt.Errorf("scan stock summary row: %w", err)
		}

		if _, exists := stockMap[variantID]; !exists {
			stockMap[variantID] = make(map[int64]float64)
		}
		stockMap[variantID][warehouseID] = totalOnHand
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stock summary rows: %w", err)
	}

	return stockMap, nil
}

// LoadOnOrder memuat quantity yang sedang dalam pesanan PO (status 'confirmed' atau 'partial').
// Mengembalikan map berstruktur map[variantID][warehouseID] = OnOrderEntry.
func LoadOnOrder(ctx context.Context, tx pgx.Tx, variantIDs []int64) (map[int64]map[int64]domain.OnOrderEntry, error) {
	onOrderMap := make(map[int64]map[int64]domain.OnOrderEntry)
	if len(variantIDs) == 0 {
		return onOrderMap, nil
	}

	rows, err := tx.Query(ctx,
		`SELECT
			poi.variant_id,
			po.warehouse_id,
			SUM(poi.quantity_pending) AS quantity_on_order,
			COALESCE(MIN(po.expected_date), CURRENT_DATE) AS earliest_arrival
		 FROM purchase_order_items poi
		 JOIN purchase_orders po ON po.id = poi.po_id
		 WHERE po.status IN ('confirmed', 'partial')
		   AND poi.quantity_pending > 0
		   AND poi.variant_id = ANY($1::bigint[])
		 GROUP BY poi.variant_id, po.warehouse_id`,
		variantIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("load on_order query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var entry domain.OnOrderEntry
		if err := rows.Scan(
			&entry.VariantID,
			&entry.WarehouseID,
			&entry.QuantityOnOrder,
			&entry.EarliestArrival,
		); err != nil {
			return nil, fmt.Errorf("scan on_order row: %w", err)
		}

		if _, exists := onOrderMap[entry.VariantID]; !exists {
			onOrderMap[entry.VariantID] = make(map[int64]domain.OnOrderEntry)
		}
		onOrderMap[entry.VariantID][entry.WarehouseID] = entry
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate on_order rows: %w", err)
	}

	return onOrderMap, nil
}
