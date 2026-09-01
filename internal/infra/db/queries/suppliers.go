package queries

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LoadSupplierHistory memuat supplier terakhir yang digunakan untuk setiap variant dari riwayat PO.
// Mengembalikan map berstruktur map[variantID]supplierID.
func LoadSupplierHistory(ctx context.Context, tx pgx.Tx) (map[int64]int64, error) {
	history := make(map[int64]int64)

	rows, err := tx.Query(ctx,
		`SELECT DISTINCT ON (poi.variant_id)
			poi.variant_id,
			po.supplier_id,
			po.po_date
		 FROM purchase_order_items poi
		 JOIN purchase_orders po ON po.id = poi.po_id
		 WHERE po.status NOT IN ('cancelled')
		 ORDER BY poi.variant_id, po.po_date DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("load supplier history query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var variantID int64
		var supplierID int64
		var poDate time.Time

		if err := rows.Scan(&variantID, &supplierID, &poDate); err != nil {
			return nil, fmt.Errorf("scan supplier history row: %w", err)
		}
		history[variantID] = supplierID
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate supplier history rows: %w", err)
	}

	return history, nil
}
