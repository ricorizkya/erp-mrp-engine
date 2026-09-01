package domain_test

import (
	"testing"
	"time"

	"erp-mrp-engine/internal/domain"
)

// generateBenchmarkDataset membuat dataset sintetis dengan nProducts, masing-masing memiliki BOM 3 level,
// dan total nDemands terdistribusi di antara produk-produk tersebut.
func generateBenchmarkDataset(nProducts, nDemands int) *domain.InputData {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	input := domain.NewInputData(1, "tenant_benchmark", now, now.AddDate(0, 3, 0))

	// Generate 3-level BOM untuk setiap produk
	// Level 1: Finished Product (varID: i*10 + 1, bomVer: i*10 + 1)
	// Level 2: Semi-Finished Good (varID: i*10 + 2, bomVer: i*10 + 2)
	// Level 3: Raw Material 1 (varID: i*10 + 3) & Raw Material 2 (varID: i*10 + 4)
	for i := 1; i <= nProducts; i++ {
		fgVarID := int64(i*10 + 1)
		fgBomVer := int64(i*10 + 1)

		semiVarID := int64(i*10 + 2)
		semiBomVer := int64(i*10 + 2)

		rawVar1 := int64(i*10 + 3)
		rawVar2 := int64(i*10 + 4)

		// Finished Good BOM
		input.ActiveBoms[fgVarID] = fgBomVer
		input.BomOutputQty[fgBomVer] = 1.0
		input.BomLeadTime[fgBomVer] = 3
		input.BomItems[fgBomVer] = []domain.BomItem{
			{ID: int64(i * 100), BomVersionID: fgBomVer, VariantID: semiVarID, QuantityWithScrap: 1.5, UomID: 1},
			{ID: int64(i*100 + 1), BomVersionID: fgBomVer, VariantID: rawVar1, QuantityWithScrap: 2.0, UomID: 2},
		}

		// Semi-Finished Good BOM
		input.ActiveBoms[semiVarID] = semiBomVer
		input.BomOutputQty[semiBomVer] = 1.0
		input.BomLeadTime[semiBomVer] = 2
		input.BomItems[semiBomVer] = []domain.BomItem{
			{ID: int64(i*100 + 2), BomVersionID: semiBomVer, VariantID: rawVar2, QuantityWithScrap: 3.0, UomID: 3},
		}

		// Set metadata variant
		input.VariantInfo[fgVarID] = domain.VariantInfo{VariantID: fgVarID, LeadTimeDays: 3, BaseUomID: 1}
		input.VariantInfo[semiVarID] = domain.VariantInfo{VariantID: semiVarID, LeadTimeDays: 2, BaseUomID: 1}
		input.VariantInfo[rawVar1] = domain.VariantInfo{VariantID: rawVar1, LeadTimeDays: 7, BaseUomID: 2}
		input.VariantInfo[rawVar2] = domain.VariantInfo{VariantID: rawVar2, LeadTimeDays: 5, BaseUomID: 3}

		// Seed partial stock untuk 50% produk
		if i%2 == 0 {
			input.StockMap[fgVarID] = map[int64]float64{1: 20.0}
			input.StockMap[semiVarID] = map[int64]float64{1: 15.0}
		}
	}

	// Generate demands terdistribusi
	for j := 1; j <= nDemands; j++ {
		productIdx := (j % nProducts) + 1
		fgVarID := int64(productIdx*10 + 1)
		neededDate := now.AddDate(0, 0, 5+(j%60))

		input.Demands = append(input.Demands, domain.MrpDemand{
			ID:          int64(j),
			VariantID:   fgVarID,
			Quantity:    float64(50 + (j % 50)),
			UomID:       1,
			NeededDate:  neededDate,
			WarehouseID: 1,
			Status:      "open",
		})
	}

	return input
}

func BenchmarkMrpCalculation_200Demands(b *testing.B) {
	input := generateBenchmarkDataset(20, 200)
	exploder := domain.NewBomExploder(input, 10)
	scheduler := domain.NewScheduler(7, 3)
	calc := domain.NewCalculator(exploder, scheduler, 0.001)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := calc.Calculate(input)
		if err != nil {
			b.Fatalf("benchmark failed: %v", err)
		}
	}
}

func BenchmarkMrpCalculation_500Demands(b *testing.B) {
	input := generateBenchmarkDataset(50, 500)
	exploder := domain.NewBomExploder(input, 10)
	scheduler := domain.NewScheduler(7, 3)
	calc := domain.NewCalculator(exploder, scheduler, 0.001)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := calc.Calculate(input)
		if err != nil {
			b.Fatalf("benchmark failed: %v", err)
		}
	}
}

func BenchmarkMrpCalculation_1000Demands(b *testing.B) {
	input := generateBenchmarkDataset(100, 1000)
	exploder := domain.NewBomExploder(input, 10)
	scheduler := domain.NewScheduler(7, 3)
	calc := domain.NewCalculator(exploder, scheduler, 0.001)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := calc.Calculate(input)
		if err != nil {
			b.Fatalf("benchmark failed: %v", err)
		}
	}
}

func BenchmarkBomExploder_DeepTree(b *testing.B) {
	input := createTestInputData()

	// 8-level deep BOM tree
	for lvl := 1; lvl <= 8; lvl++ {
		verID := int64(lvl)
		nextVarID := int64(lvl + 1)
		input.BomOutputQty[verID] = 1.0
		input.ActiveBoms[int64(lvl)] = verID

		if lvl < 8 {
			input.ActiveBoms[nextVarID] = int64(lvl + 1)
			input.BomItems[verID] = []domain.BomItem{
				{ID: int64(lvl), BomVersionID: verID, VariantID: nextVarID, IsPhantom: true, QuantityWithScrap: 2.0},
			}
		} else {
			input.BomItems[verID] = []domain.BomItem{
				{ID: int64(lvl), BomVersionID: verID, VariantID: 9999, IsPhantom: false, QuantityWithScrap: 1.0},
			}
		}
	}

	exploder := domain.NewBomExploder(input, 10)
	neededDate := time.Now().AddDate(0, 0, 30)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := exploder.Explode(1, 100.0, neededDate, 1)
		if err != nil {
			b.Fatalf("benchmark explode failed: %v", err)
		}
	}
}
