package queries

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-mrp-engine/internal/domain"
)

// BulkInsertPlannedOrders memasukkan kumpulan planned orders secara batch menggunakan PostgreSQL UNNEST.
// Fungsi ini mengembalikan daftar ID hasil insert via RETURNING id secara berurutan sesuai input.
func BulkInsertPlannedOrders(ctx context.Context, tx pgx.Tx, orders []domain.PlannedOrder) ([]int64, error) {
	if len(orders) == 0 {
		return []int64{}, nil
	}

	n := len(orders)
	mrpRunIDs := make([]int64, n)
	variantIDs := make([]int64, n)
	orderTypes := make([]string, n)
	quantities := make([]float64, n)
	uomIDs := make([]int64, n)
	plannedStarts := make([]time.Time, n)
	plannedFinishes := make([]time.Time, n)
	bomVersionIDs := make([]*int64, n)
	suggestedSupplierIDs := make([]*int64, n)
	statuses := make([]string, n)
	createdAts := make([]time.Time, n)
	updatedAts := make([]time.Time, n)

	now := time.Now()
	for i, o := range orders {
		mrpRunIDs[i] = o.MrpRunID
		variantIDs[i] = o.VariantID
		orderTypes[i] = string(o.OrderType)
		quantities[i] = o.Quantity
		uomIDs[i] = o.UomID
		plannedStarts[i] = o.PlannedStart
		plannedFinishes[i] = o.PlannedFinish
		bomVersionIDs[i] = o.BomVersionID
		suggestedSupplierIDs[i] = o.SuggestedSupplierID

		status := o.Status
		if status == "" {
			status = domain.PlannedOrderStatusProposed
		}
		statuses[i] = status
		createdAts[i] = now
		updatedAts[i] = now
	}

	rows, err := tx.Query(ctx,
		`INSERT INTO planned_orders (
			mrp_run_id,
			variant_id,
			order_type,
			quantity,
			uom_id,
			planned_start,
			planned_finish,
			bom_version_id,
			suggested_supplier_id,
			status,
			created_at,
			updated_at
		 )
		 SELECT * FROM UNNEST(
			$1::bigint[],
			$2::bigint[],
			$3::text[],
			$4::numeric[],
			$5::bigint[],
			$6::date[],
			$7::date[],
			$8::bigint[],
			$9::bigint[],
			$10::text[],
			$11::timestamptz[],
			$12::timestamptz[]
		 )
		 RETURNING id`,
		mrpRunIDs,
		variantIDs,
		orderTypes,
		quantities,
		uomIDs,
		plannedStarts,
		plannedFinishes,
		bomVersionIDs,
		suggestedSupplierIDs,
		statuses,
		createdAts,
		updatedAts,
	)
	if err != nil {
		return nil, fmt.Errorf("bulk insert planned orders: %w", err)
	}
	defer rows.Close()

	insertedIDs := make([]int64, 0, n)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan inserted planned order id: %w", err)
		}
		insertedIDs = append(insertedIDs, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inserted planned order ids: %w", err)
	}

	return insertedIDs, nil
}

// BulkInsertPlannedOrderDemands memasukkan relasi link antara planned order dengan mrp_demands.
func BulkInsertPlannedOrderDemands(ctx context.Context, tx pgx.Tx, links []domain.PlannedOrderDemand) error {
	if len(links) == 0 {
		return nil
	}

	n := len(links)
	plannedOrderIDs := make([]int64, n)
	demandIDs := make([]int64, n)
	quantitiesAllocated := make([]float64, n)

	for i, link := range links {
		plannedOrderIDs[i] = link.PlannedOrderID
		demandIDs[i] = link.DemandID
		quantitiesAllocated[i] = link.QuantityAllocated
	}

	_, err := tx.Exec(ctx,
		`INSERT INTO planned_order_demands (
			planned_order_id,
			demand_id,
			quantity_allocated
		 )
		 SELECT * FROM UNNEST(
			$1::bigint[],
			$2::bigint[],
			$3::numeric[]
		 )`,
		plannedOrderIDs,
		demandIDs,
		quantitiesAllocated,
	)
	if err != nil {
		return fmt.Errorf("bulk insert planned order demands: %w", err)
	}

	return nil
}

// BulkUpdateDemandStatus memperbarui status daftar mrp_demands menjadi 'planned'.
func BulkUpdateDemandStatus(ctx context.Context, tx pgx.Tx, demandIDs []int64, status string) error {
	if len(demandIDs) == 0 {
		return nil
	}

	_, err := tx.Exec(ctx,
		`UPDATE mrp_demands
		 SET status = $2, updated_at = NOW()
		 WHERE id = ANY($1::bigint[])`,
		demandIDs,
		status,
	)
	if err != nil {
		return fmt.Errorf("bulk update demand status to %s: %w", status, err)
	}

	return nil
}
