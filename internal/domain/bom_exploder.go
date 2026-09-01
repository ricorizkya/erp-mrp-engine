package domain

import (
	"fmt"
	"time"
)

// BomExploder melakukan dekomposisi Bill of Materials (BOM)
// menjadi daftar kebutuhan sub-komponen (SubDemand).
type BomExploder struct {
	input    *InputData
	maxDepth int
}

// NewBomExploder membuat instance BomExploder baru.
func NewBomExploder(input *InputData, maxDepth int) *BomExploder {
	if maxDepth <= 0 {
		maxDepth = 10
	}
	return &BomExploder{
		input:    input,
		maxDepth: maxDepth,
	}
}

// Explode menjalankan dekomposisi untuk suatu versi BOM dan quantity produk induk yang dibutuhkan.
// Item berstatus phantom akan di-explode tembus langsung ke sub-BOM-nya.
// Komponen yang bukan phantom akan menghasilkan SubDemand (yang nantinya jika merupakan semi-finished good
// akan dihitung net requirement-nya oleh Calculator sebelum sub-BOM-nya di-explode).
func (e *BomExploder) Explode(
	bomVersionID int64,
	parentQuantity float64,
	neededDate time.Time,
	warehouseID int64,
) ([]SubDemand, error) {
	if parentQuantity <= 0 {
		return []SubDemand{}, nil
	}

	visited := make(map[int64]bool)
	var subDemands []SubDemand

	err := e.explodeRecursive(
		bomVersionID,
		parentQuantity,
		neededDate,
		warehouseID,
		1,
		visited,
		&subDemands,
	)
	if err != nil {
		return nil, err
	}

	return subDemands, nil
}

func (e *BomExploder) explodeRecursive(
	bomVersionID int64,
	parentQuantity float64,
	neededDate time.Time,
	warehouseID int64,
	depth int,
	visited map[int64]bool,
	subDemands *[]SubDemand,
) error {
	if depth > e.maxDepth {
		return fmt.Errorf("max BOM depth (%d) exceeded at bom version %d", e.maxDepth, bomVersionID)
	}

	if visited[bomVersionID] {
		return fmt.Errorf("circular BOM reference detected at bom version %d", bomVersionID)
	}
	visited[bomVersionID] = true
	defer func() {
		delete(visited, bomVersionID)
	}()

	items, exists := e.input.BomItems[bomVersionID]
	if !exists || len(items) == 0 {
		return nil
	}

	outputQty := e.input.BomOutputQty[bomVersionID]
	if outputQty <= 0 {
		outputQty = 1.0
	}
	ratio := parentQuantity / outputQty

	for _, item := range items {
		// Hanya proses item tingkat atas dari BOM versi ini (parent_item_id IS NULL)
		if item.ParentItemID != nil {
			continue
		}

		qtyWithScrap := item.QuantityWithScrap
		if qtyWithScrap <= 0 {
			qtyWithScrap = item.Quantity * (1.0 + (item.ScrapPct / 100.0))
		}
		requiredQty := ratio * qtyWithScrap

		if item.IsPhantom {
			// Phantom item: tidak di-stok dan tidak dibuatkan order, langsung tembus ke sub-BOM-nya
			subBomID, hasSubBom := e.input.ActiveBoms[item.VariantID]
			if hasSubBom {
				if err := e.explodeRecursive(
					subBomID,
					requiredQty,
					neededDate,
					warehouseID,
					depth+1,
					visited,
					subDemands,
				); err != nil {
					return err
				}
			}
			continue
		}

		// Non-phantom item: tambahkan sebagai SubDemand
		*subDemands = append(*subDemands, SubDemand{
			VariantID:       item.VariantID,
			Quantity:        requiredQty,
			UomID:           item.UomID,
			NeededDate:      neededDate,
			WarehouseID:     warehouseID,
			ParentVariantID: item.VariantID,
			Depth:           depth,
		})
	}

	return nil
}
