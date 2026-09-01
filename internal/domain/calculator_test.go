package domain_test

import (
	"testing"
	"time"

	"erp-mrp-engine/internal/domain"
)

func setupCalculator(input *domain.InputData) *domain.Calculator {
	exploder := domain.NewBomExploder(input, 10)
	scheduler := domain.NewScheduler(7, 3)
	return domain.NewCalculator(exploder, scheduler, 0.001)
}

// 1. Sufficient on-hand stock -> 0 planned orders
func TestCalculate_SufficientOnHand(t *testing.T) {
	input := createTestInputData()
	varID := int64(10)
	whID := int64(1)

	input.Demands = []domain.MrpDemand{
		{ID: 1, VariantID: varID, WarehouseID: whID, Quantity: 50.0, NeededDate: time.Now().AddDate(0, 0, 10)},
	}

	input.StockMap[varID] = map[int64]float64{whID: 80.0}

	calc := setupCalculator(input)
	res, err := calc.Calculate(input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.PlannedOrders) != 0 {
		t.Errorf("expected 0 planned orders, got %d", len(res.PlannedOrders))
	}
	if len(res.FulfilledDemandIDs) != 1 || res.FulfilledDemandIDs[0] != 1 {
		t.Errorf("expected demand 1 fulfilled, got %v", res.FulfilledDemandIDs)
	}
}

// 2. Sufficient on-order -> 0 planned orders
func TestCalculate_SufficientOnOrder(t *testing.T) {
	input := createTestInputData()
	varID := int64(11)
	whID := int64(1)

	input.Demands = []domain.MrpDemand{
		{ID: 2, VariantID: varID, WarehouseID: whID, Quantity: 40.0, NeededDate: time.Now().AddDate(0, 0, 10)},
	}
	input.OnOrderMap[varID] = map[int64]domain.OnOrderEntry{
		whID: {VariantID: varID, WarehouseID: whID, QuantityOnOrder: 50.0},
	}

	calc := setupCalculator(input)
	res, err := calc.Calculate(input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.PlannedOrders) != 0 {
		t.Errorf("expected 0 planned orders, got %d", len(res.PlannedOrders))
	}
	if len(res.FulfilledDemandIDs) != 1 || res.FulfilledDemandIDs[0] != 2 {
		t.Errorf("expected demand 2 fulfilled, got %v", res.FulfilledDemandIDs)
	}
}

// 3. Partial stock (on-hand 40, demand 100) -> planned order for 60
func TestCalculate_PartialStock(t *testing.T) {
	input := createTestInputData()
	varID := int64(12)
	whID := int64(1)

	input.Demands = []domain.MrpDemand{
		{ID: 3, VariantID: varID, WarehouseID: whID, Quantity: 100.0, NeededDate: time.Now().AddDate(0, 0, 10)},
	}
	input.StockMap[varID] = map[int64]float64{whID: 40.0}

	calc := setupCalculator(input)
	res, err := calc.Calculate(input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.PlannedOrders) != 1 {
		t.Fatalf("expected 1 planned order, got %d", len(res.PlannedOrders))
	}
	if res.PlannedOrders[0].Quantity != 60.0 {
		t.Errorf("expected planned order qty 60.0, got %f", res.PlannedOrders[0].Quantity)
	}
	if res.PlannedOrders[0].OrderType != domain.OrderTypePurchase {
		t.Errorf("expected OrderTypePurchase, got %v", res.PlannedOrders[0].OrderType)
	}
}

// 4. No BOM -> Planned Purchase Order
func TestCalculate_NoBom(t *testing.T) {
	input := createTestInputData()
	varID := int64(13)
	whID := int64(1)

	input.Demands = []domain.MrpDemand{
		{ID: 4, VariantID: varID, WarehouseID: whID, Quantity: 25.0, NeededDate: time.Now().AddDate(0, 0, 10)},
	}
	input.SupplierHistory[varID] = 888 // Last supplier ID

	calc := setupCalculator(input)
	res, err := calc.Calculate(input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.PlannedOrders) != 1 {
		t.Fatalf("expected 1 planned order, got %d", len(res.PlannedOrders))
	}
	order := res.PlannedOrders[0]
	if order.OrderType != domain.OrderTypePurchase {
		t.Errorf("expected purchase order, got %v", order.OrderType)
	}
	if order.SuggestedSupplierID == nil || *order.SuggestedSupplierID != 888 {
		t.Errorf("expected suggested supplier 888, got %v", order.SuggestedSupplierID)
	}
}

// 5. With BOM -> Planned Production Order + Sub-demands for raw materials
func TestCalculate_WithBom(t *testing.T) {
	input := createTestInputData()
	varFinished := int64(14)
	varRaw := int64(15)
	whID := int64(1)
	bomVer := int64(501)

	input.Demands = []domain.MrpDemand{
		{ID: 5, VariantID: varFinished, WarehouseID: whID, Quantity: 10.0, NeededDate: time.Now().AddDate(0, 0, 15)},
	}

	input.ActiveBoms[varFinished] = bomVer
	input.BomOutputQty[bomVer] = 1.0
	input.BomLeadTime[bomVer] = 4
	input.BomItems[bomVer] = []domain.BomItem{
		{ID: 1, BomVersionID: bomVer, VariantID: varRaw, QuantityWithScrap: 2.5, UomID: 2},
	}

	calc := setupCalculator(input)
	res, err := calc.Calculate(input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Harusnya ada 2 planned orders:
	// 1. Production Finished Good (qty: 10)
	// 2. Purchase Raw Material (qty: 10 * 2.5 = 25)
	if len(res.PlannedOrders) != 2 {
		t.Fatalf("expected 2 planned orders, got %d", len(res.PlannedOrders))
	}

	prodOrder := res.PlannedOrders[0]
	if prodOrder.OrderType != domain.OrderTypeProduction || prodOrder.VariantID != varFinished || prodOrder.Quantity != 10.0 {
		t.Errorf("unexpected production order: %+v", prodOrder)
	}

	purchOrder := res.PlannedOrders[1]
	if purchOrder.OrderType != domain.OrderTypePurchase || purchOrder.VariantID != varRaw || purchOrder.Quantity != 25.0 {
		t.Errorf("unexpected purchase order: %+v", purchOrder)
	}
}

// 6. Multiple Demands Same Variant -> Aggregated single planned order with covered demands
func TestCalculate_MultipleDemandsSameVariant(t *testing.T) {
	input := createTestInputData()
	varID := int64(16)
	whID := int64(1)

	d1 := time.Now().AddDate(0, 0, 10)
	d2 := time.Now().AddDate(0, 0, 15)
	d3 := time.Now().AddDate(0, 0, 5)

	input.Demands = []domain.MrpDemand{
		{ID: 101, VariantID: varID, WarehouseID: whID, Quantity: 20.0, NeededDate: d1},
		{ID: 102, VariantID: varID, WarehouseID: whID, Quantity: 30.0, NeededDate: d2},
		{ID: 103, VariantID: varID, WarehouseID: whID, Quantity: 50.0, NeededDate: d3},
	}

	calc := setupCalculator(input)
	res, err := calc.Calculate(input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.PlannedOrders) != 1 {
		t.Fatalf("expected 1 aggregated planned order, got %d", len(res.PlannedOrders))
	}

	order := res.PlannedOrders[0]
	if order.Quantity != 100.0 {
		t.Errorf("expected total aggregated qty 100.0, got %f", order.Quantity)
	}
	if len(order.CoveredDemands) != 3 {
		t.Errorf("expected 3 covered demands linked, got %d", len(order.CoveredDemands))
	}
}

// 7. Multi-Level BOM -> Production chain and raw purchase
func TestCalculate_MultiLevelBom(t *testing.T) {
	input := createTestInputData()

	varKaos := int64(100)
	varKain := int64(200)
	varBenang := int64(300)
	whID := int64(1)

	bomKaos := int64(1)
	bomKain := int64(2)

	input.Demands = []domain.MrpDemand{
		{ID: 1, VariantID: varKaos, WarehouseID: whID, Quantity: 100.0, NeededDate: time.Now().AddDate(0, 0, 20)},
	}

	// Kaos -> Kain Jadi
	input.ActiveBoms[varKaos] = bomKaos
	input.BomOutputQty[bomKaos] = 1.0
	input.BomItems[bomKaos] = []domain.BomItem{
		{ID: 1, BomVersionID: bomKaos, VariantID: varKain, QuantityWithScrap: 0.3},
	}

	// Kain Jadi -> Benang
	input.ActiveBoms[varKain] = bomKain
	input.BomOutputQty[bomKain] = 1.0
	input.BomItems[bomKain] = []domain.BomItem{
		{ID: 2, BomVersionID: bomKain, VariantID: varBenang, QuantityWithScrap: 1.1},
	}

	calc := setupCalculator(input)
	res, err := calc.Calculate(input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1. Planned Production Kaos (100)
	// 2. Planned Production Kain Jadi (30)
	// 3. Planned Purchase Benang (33)
	if len(res.PlannedOrders) != 3 {
		t.Fatalf("expected 3 planned orders across multi-level BOM, got %d", len(res.PlannedOrders))
	}

	if res.Stats.TotalProduction != 2 {
		t.Errorf("expected 2 production orders, got %d", res.Stats.TotalProduction)
	}
	if res.Stats.TotalPurchase != 1 {
		t.Errorf("expected 1 purchase order, got %d", res.Stats.TotalPurchase)
	}
}

// 8. Mixed MTO / MTS demands
func TestCalculate_MixedMtoMts(t *testing.T) {
	input := createTestInputData()
	whID := int64(1)

	varA := int64(1) // has BOM
	varB := int64(2) // no BOM

	input.Demands = []domain.MrpDemand{
		{ID: 1, VariantID: varA, WarehouseID: whID, Quantity: 10.0, DemandType: "so", NeededDate: time.Now().AddDate(0, 0, 10)},
		{ID: 2, VariantID: varB, WarehouseID: whID, Quantity: 20.0, DemandType: "safety_stock", NeededDate: time.Now().AddDate(0, 0, 10)},
	}

	input.ActiveBoms[varA] = 10
	input.BomOutputQty[10] = 1.0
	input.BomItems[10] = []domain.BomItem{} // Simple finished good

	calc := setupCalculator(input)
	res, err := calc.Calculate(input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.PlannedOrders) != 2 {
		t.Fatalf("expected 2 planned orders, got %d", len(res.PlannedOrders))
	}
	if res.Stats.TotalProduction != 1 || res.Stats.TotalPurchase != 1 {
		t.Errorf("expected 1 prod and 1 purch, got %d prod and %d purch", res.Stats.TotalProduction, res.Stats.TotalPurchase)
	}
}
