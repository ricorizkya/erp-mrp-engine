package domain

import (
	"fmt"
	"time"

	"erp-mrp-engine/internal/utils"
)

// Calculator mengorkestrasi kalkulasi kebutuhan bersih (Net Requirement)
// dan pembuatan Planned Orders (Produksi / Pembelian).
type Calculator struct {
	exploder  *BomExploder
	scheduler *Scheduler
	minQty    float64
}

// NewCalculator membuat instance Calculator baru.
func NewCalculator(exploder *BomExploder, scheduler *Scheduler, minQty float64) *Calculator {
	if minQty <= 0 {
		minQty = 0.001
	}
	return &Calculator{
		exploder:  exploder,
		scheduler: scheduler,
		minQty:    minQty,
	}
}

// Calculate mengeksekusi perhitungan MRP secara lengkap berdasarkan InputData.
func (c *Calculator) Calculate(input *InputData) (*CalcResult, error) {
	if input == nil {
		return nil, fmt.Errorf("input data cannot be nil")
	}

	result := &CalcResult{
		PlannedOrders:      []PlannedOrder{},
		FulfilledDemandIDs: []int64{},
		PlannedDemandIDs:   []int64{},
		Stats:              CalcStats{},
	}

	// Buat salinan stok lokal agar pengurangan stok selama iterasi tidak memutasi map input langsung
	remainingOnHand := make(map[int64]map[int64]float64)
	for v, whMap := range input.StockMap {
		remainingOnHand[v] = make(map[int64]float64)
		for w, qty := range whMap {
			remainingOnHand[v][w] = qty
		}
	}

	remainingOnOrder := make(map[int64]map[int64]float64)
	for v, whMap := range input.OnOrderMap {
		remainingOnOrder[v] = make(map[int64]float64)
		for w, entry := range whMap {
			remainingOnOrder[v][w] = entry.QuantityOnOrder
		}
	}

	// 1. Proses Initial Demands (dari Sales Order, Forecast, atau Safety Stock)
	initialGroups := c.groupDemands(input.Demands)

	var subDemandsQueue []SubDemand

	for _, group := range initialGroups {
		netQty := c.deductInventory(group.VariantID, group.WarehouseID, group.TotalQuantity, remainingOnHand, remainingOnOrder)

		if netQty <= c.minQty {
			// Kebutuhan terpenuhi dari stok on-hand atau on-order
			for _, d := range group.Demands {
				result.FulfilledDemandIDs = append(result.FulfilledDemandIDs, d.ID)
			}
			result.Stats.TotalFulfilled += len(group.Demands)
			continue
		}

		// Kebutuhan bersih > 0: Buat Planned Order
		for _, d := range group.Demands {
			result.PlannedDemandIDs = append(result.PlannedDemandIDs, d.ID)
		}

		uomID := group.Demands[0].UomID
		if uomID == 0 {
			if info, exists := input.VariantInfo[group.VariantID]; exists {
				uomID = info.BaseUomID
			}
		}

		bomVerID, hasBom := input.ActiveBoms[group.VariantID]
		if hasBom {
			// Planned Production Order
			sched := c.scheduler.CalculateProductionSchedule(group.EarliestNeededDate, bomVerID, input)
			order := PlannedOrder{
				MrpRunID:       input.RunID,
				VariantID:      group.VariantID,
				WarehouseID:    group.WarehouseID,
				OrderType:      OrderTypeProduction,
				Quantity:       netQty,
				UomID:          uomID,
				PlannedStart:   sched.PlannedStart,
				PlannedFinish:  sched.PlannedFinish,
				BomVersionID:   &bomVerID,
				IsOverdue:      sched.IsOverdue,
				Status:         PlannedOrderStatusProposed,
				CoveredDemands: make([]PlannedOrderDemand, 0, len(group.Demands)),
			}

			for _, d := range group.Demands {
				order.CoveredDemands = append(order.CoveredDemands, PlannedOrderDemand{
					DemandID:          d.ID,
					QuantityAllocated: d.Quantity,
				})
			}

			result.PlannedOrders = append(result.PlannedOrders, order)
			result.Stats.TotalProduction++
			if sched.IsOverdue {
				result.Stats.OverdueOrders++
			}

			// Explode BOM untuk komponen anak. Tanggal dibutuhkan adalah saat produksi dimulai (PlannedStart)
			children, err := c.exploder.Explode(bomVerID, netQty, sched.PlannedStart, group.WarehouseID)
			if err != nil {
				return nil, fmt.Errorf("explode BOM for variant %d (run %d): %w", group.VariantID, input.RunID, err)
			}
			subDemandsQueue = append(subDemandsQueue, children...)

		} else {
			// Planned Purchase Order
			sched := c.scheduler.CalculatePurchaseSchedule(group.EarliestNeededDate, group.VariantID, input)
			var supplierIDPtr *int64
			if suppID, hasSupp := input.SupplierHistory[group.VariantID]; hasSupp && suppID > 0 {
				supplierIDPtr = &suppID
			}

			order := PlannedOrder{
				MrpRunID:            input.RunID,
				VariantID:           group.VariantID,
				WarehouseID:         group.WarehouseID,
				OrderType:           OrderTypePurchase,
				Quantity:            netQty,
				UomID:               uomID,
				PlannedStart:        sched.PlannedStart,
				PlannedFinish:       sched.PlannedFinish,
				SuggestedSupplierID: supplierIDPtr,
				IsOverdue:           sched.IsOverdue,
				Status:              PlannedOrderStatusProposed,
				CoveredDemands:      make([]PlannedOrderDemand, 0, len(group.Demands)),
			}

			for _, d := range group.Demands {
				order.CoveredDemands = append(order.CoveredDemands, PlannedOrderDemand{
					DemandID:          d.ID,
					QuantityAllocated: d.Quantity,
				})
			}

			result.PlannedOrders = append(result.PlannedOrders, order)
			result.Stats.TotalPurchase++
			if sched.IsOverdue {
				result.Stats.OverdueOrders++
			}
		}
	}

	// 2. Proses Sub-Demands yang dihasilkan dari BOM explosion
	for len(subDemandsQueue) > 0 {
		currentBatch := subDemandsQueue
		subDemandsQueue = nil

		subGroups := c.groupSubDemands(currentBatch)
		for _, subGroup := range subGroups {
			netQty := c.deductInventory(subGroup.VariantID, subGroup.WarehouseID, subGroup.TotalQuantity, remainingOnHand, remainingOnOrder)
			if netQty <= c.minQty {
				continue
			}

			uomID := subGroup.UomID
			if uomID == 0 {
				if info, exists := input.VariantInfo[subGroup.VariantID]; exists {
					uomID = info.BaseUomID
				}
			}

			bomVerID, hasBom := input.ActiveBoms[subGroup.VariantID]
			if hasBom {
				sched := c.scheduler.CalculateProductionSchedule(subGroup.EarliestNeededDate, bomVerID, input)
				order := PlannedOrder{
					MrpRunID:      input.RunID,
					VariantID:     subGroup.VariantID,
					WarehouseID:   subGroup.WarehouseID,
					OrderType:     OrderTypeProduction,
					Quantity:      netQty,
					UomID:         uomID,
					PlannedStart:  sched.PlannedStart,
					PlannedFinish: sched.PlannedFinish,
					BomVersionID:  &bomVerID,
					IsOverdue:     sched.IsOverdue,
					Status:        PlannedOrderStatusProposed,
				}

				result.PlannedOrders = append(result.PlannedOrders, order)
				result.Stats.TotalProduction++
				if sched.IsOverdue {
					result.Stats.OverdueOrders++
				}

				children, err := c.exploder.Explode(bomVerID, netQty, sched.PlannedStart, subGroup.WarehouseID)
				if err != nil {
					return nil, fmt.Errorf("explode sub-BOM for variant %d (run %d): %w", subGroup.VariantID, input.RunID, err)
				}
				subDemandsQueue = append(subDemandsQueue, children...)

			} else {
				sched := c.scheduler.CalculatePurchaseSchedule(subGroup.EarliestNeededDate, subGroup.VariantID, input)
				var supplierIDPtr *int64
				if suppID, hasSupp := input.SupplierHistory[subGroup.VariantID]; hasSupp && suppID > 0 {
					supplierIDPtr = &suppID
				}

				order := PlannedOrder{
					MrpRunID:            input.RunID,
					VariantID:           subGroup.VariantID,
					WarehouseID:         subGroup.WarehouseID,
					OrderType:           OrderTypePurchase,
					Quantity:            netQty,
					UomID:               uomID,
					PlannedStart:        sched.PlannedStart,
					PlannedFinish:       sched.PlannedFinish,
					SuggestedSupplierID: supplierIDPtr,
					IsOverdue:           sched.IsOverdue,
					Status:              PlannedOrderStatusProposed,
				}

				result.PlannedOrders = append(result.PlannedOrders, order)
				result.Stats.TotalPurchase++
				if sched.IsOverdue {
					result.Stats.OverdueOrders++
				}
			}
		}
	}

	return result, nil
}

func (c *Calculator) deductInventory(
	variantID, warehouseID int64,
	grossQty float64,
	onHandMap map[int64]map[int64]float64,
	onOrderMap map[int64]map[int64]float64,
) float64 {
	rem := grossQty

	// 1. Kurangi dari on-hand stock
	if whMap, ok := onHandMap[variantID]; ok {
		if onHand, hasStock := whMap[warehouseID]; hasStock && onHand > 0 {
			if onHand >= rem {
				whMap[warehouseID] = onHand - rem
				return 0
			}
			rem -= onHand
			whMap[warehouseID] = 0
		}
	}

	// 2. Kurangi dari on-order purchase order
	if whMap, ok := onOrderMap[variantID]; ok {
		if onOrder, hasOnOrder := whMap[warehouseID]; hasOnOrder && onOrder > 0 {
			if onOrder >= rem {
				whMap[warehouseID] = onOrder - rem
				return 0
			}
			rem -= onOrder
			whMap[warehouseID] = 0
		}
	}

	return rem
}

func (c *Calculator) groupDemands(demands []MrpDemand) []DemandGroup {
	groupsMap := make(map[string]*DemandGroup)
	var orderedKeys []string

	for _, d := range demands {
		key := fmt.Sprintf("%d_%d", d.VariantID, d.WarehouseID)
		g, exists := groupsMap[key]
		if !exists {
			g = &DemandGroup{
				VariantID:          d.VariantID,
				WarehouseID:        d.WarehouseID,
				TotalQuantity:      0,
				Demands:            []MrpDemand{},
				EarliestNeededDate: d.NeededDate,
			}
			groupsMap[key] = g
			orderedKeys = append(orderedKeys, key)
		}

		g.TotalQuantity += d.Quantity
		g.Demands = append(g.Demands, d)
		g.EarliestNeededDate = utils.MinTime(g.EarliestNeededDate, d.NeededDate)
	}

	result := make([]DemandGroup, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		result = append(result, *groupsMap[key])
	}
	return result
}

type subDemandGroup struct {
	VariantID          int64
	WarehouseID        int64
	TotalQuantity      float64
	EarliestNeededDate time.Time
	UomID              int64
}

func (c *Calculator) groupSubDemands(demands []SubDemand) []subDemandGroup {
	groupsMap := make(map[string]*subDemandGroup)
	var orderedKeys []string

	for _, sd := range demands {
		key := fmt.Sprintf("%d_%d", sd.VariantID, sd.WarehouseID)
		g, exists := groupsMap[key]
		if !exists {
			g = &subDemandGroup{
				VariantID:          sd.VariantID,
				WarehouseID:        sd.WarehouseID,
				TotalQuantity:      0,
				EarliestNeededDate: sd.NeededDate,
				UomID:              sd.UomID,
			}
			groupsMap[key] = g
			orderedKeys = append(orderedKeys, key)
		}

		g.TotalQuantity += sd.Quantity
		g.EarliestNeededDate = utils.MinTime(g.EarliestNeededDate, sd.NeededDate)
	}

	result := make([]subDemandGroup, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		result = append(result, *groupsMap[key])
	}
	return result
}
