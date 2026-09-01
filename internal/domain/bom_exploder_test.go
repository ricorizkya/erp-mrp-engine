package domain_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"erp-mrp-engine/internal/domain"
)

func createTestInputData() *domain.InputData {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.NewInputData(1, "tenant_companyabc", now, now.AddDate(0, 3, 0))
}

// 1. Single-level BOM, qty bulat
func TestExplode_SingleLevel(t *testing.T) {
	input := createTestInputData()
	bomVerID := int64(101)
	input.BomOutputQty[bomVerID] = 1.0
	input.BomItems[bomVerID] = []domain.BomItem{
		{ID: 1, BomVersionID: bomVerID, VariantID: 201, Quantity: 2.0, QuantityWithScrap: 2.0, UomID: 1},
		{ID: 2, BomVersionID: bomVerID, VariantID: 202, Quantity: 5.0, QuantityWithScrap: 5.0, UomID: 1},
	}

	exploder := domain.NewBomExploder(input, 10)
	neededDate := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	subDemands, err := exploder.Explode(bomVerID, 10.0, neededDate, 1)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(subDemands) != 2 {
		t.Fatalf("expected 2 sub-demands, got %d", len(subDemands))
	}

	if subDemands[0].VariantID != 201 || subDemands[0].Quantity != 20.0 {
		t.Errorf("expected item 201 qty 20.0, got variant %d qty %f", subDemands[0].VariantID, subDemands[0].Quantity)
	}
	if subDemands[1].VariantID != 202 || subDemands[1].Quantity != 50.0 {
		t.Errorf("expected item 202 qty 50.0, got variant %d qty %f", subDemands[1].VariantID, subDemands[1].Quantity)
	}
}

// 2. Multi-level BOM through Phantom components (3+ level nested)
func TestExplode_MultiLevel(t *testing.T) {
	input := createTestInputData()

	// Level 1: Kaos (Ver 1) -> Phantom Sub-assembly 1 (Var 20, Ver 2)
	ver1 := int64(1)
	ver2 := int64(2)
	ver3 := int64(3)

	input.BomOutputQty[ver1] = 1.0
	input.BomItems[ver1] = []domain.BomItem{
		{ID: 1, BomVersionID: ver1, VariantID: 20, IsPhantom: true, QuantityWithScrap: 1.5, UomID: 1},
	}
	input.ActiveBoms[20] = ver2

	// Level 2: Phantom Sub-assembly 1 (Ver 2) -> Phantom Sub-assembly 2 (Var 30, Ver 3)
	input.BomOutputQty[ver2] = 1.0
	input.BomItems[ver2] = []domain.BomItem{
		{ID: 2, BomVersionID: ver2, VariantID: 30, IsPhantom: true, QuantityWithScrap: 2.0, UomID: 1},
	}
	input.ActiveBoms[30] = ver3

	// Level 3: Phantom Sub-assembly 2 (Ver 3) -> Kapas Mentah (Var 40, Raw material)
	input.BomOutputQty[ver3] = 1.0
	input.BomItems[ver3] = []domain.BomItem{
		{ID: 3, BomVersionID: ver3, VariantID: 40, IsPhantom: false, QuantityWithScrap: 1.2, UomID: 1},
	}

	exploder := domain.NewBomExploder(input, 10)
	neededDate := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	subDemands, err := exploder.Explode(ver1, 10.0, neededDate, 1)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Karena Var 20 & 30 adalah phantom, yang dihasilkan adalah raw material Var 40: 10 * 1.5 * 2.0 * 1.2 = 36.0
	if len(subDemands) != 1 {
		t.Fatalf("expected 1 final leaf sub-demand, got %d", len(subDemands))
	}

	if subDemands[0].VariantID != 40 || math.Abs(subDemands[0].Quantity-36.0) > 0.001 {
		t.Errorf("expected variant 40 qty 36.0, got variant %d qty %f", subDemands[0].VariantID, subDemands[0].Quantity)
	}
}

// 3. Qty pecahan (rounding error tidak boleh terakumulasi)
func TestExplode_FractionalQty(t *testing.T) {
	input := createTestInputData()
	bomVerID := int64(102)
	input.BomOutputQty[bomVerID] = 3.0
	input.BomItems[bomVerID] = []domain.BomItem{
		{ID: 1, BomVersionID: bomVerID, VariantID: 301, QuantityWithScrap: 1.125, UomID: 1},
	}

	exploder := domain.NewBomExploder(input, 10)
	neededDate := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	subDemands, err := exploder.Explode(bomVerID, 7.0, neededDate, 1)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedQty := (7.0 / 3.0) * 1.125
	if math.Abs(subDemands[0].Quantity-expectedQty) > 0.0001 {
		t.Errorf("expected qty %f, got %f", expectedQty, subDemands[0].Quantity)
	}
}

// 4. Circular BOM reference — harus return error, bukan infinite loop
func TestExplode_CircularDetection(t *testing.T) {
	input := createTestInputData()

	verA := int64(11)
	verB := int64(12)

	// A membutuhkan B (phantom)
	input.BomOutputQty[verA] = 1.0
	input.BomItems[verA] = []domain.BomItem{
		{ID: 1, BomVersionID: verA, VariantID: 1002, IsPhantom: true, QuantityWithScrap: 1.0},
	}
	input.ActiveBoms[1002] = verB

	// B membutuhkan A (phantom circular!)
	input.BomOutputQty[verB] = 1.0
	input.BomItems[verB] = []domain.BomItem{
		{ID: 2, BomVersionID: verB, VariantID: 1001, IsPhantom: true, QuantityWithScrap: 1.0},
	}
	input.ActiveBoms[1001] = verA

	exploder := domain.NewBomExploder(input, 10)
	_, err := exploder.Explode(verA, 1.0, time.Now(), 1)

	if err == nil {
		t.Fatal("expected circular reference error, got nil")
	}

	if !strings.Contains(err.Error(), "circular BOM reference detected") {
		t.Errorf("expected circular reference message, got %v", err)
	}
}

// 5. BOM versioning — memakai versi yang ditentukan
func TestExplode_BomVersioning(t *testing.T) {
	input := createTestInputData()

	verV1 := int64(201)
	verV2 := int64(202)

	input.BomOutputQty[verV1] = 1.0
	input.BomItems[verV1] = []domain.BomItem{
		{ID: 1, BomVersionID: verV1, VariantID: 501, QuantityWithScrap: 10.0},
	}

	input.BomOutputQty[verV2] = 1.0
	input.BomItems[verV2] = []domain.BomItem{
		{ID: 2, BomVersionID: verV2, VariantID: 501, QuantityWithScrap: 8.0}, // versi revisi lebih efisien
	}

	exploder := domain.NewBomExploder(input, 10)
	subDemands, err := exploder.Explode(verV2, 5.0, time.Now(), 1)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if subDemands[0].Quantity != 40.0 {
		t.Errorf("expected 40.0 (5 * 8.0 from v2), got %f", subDemands[0].Quantity)
	}
}

// 6. Direct component requirement with scrap
func TestExplode_ScrapCalculation(t *testing.T) {
	input := createTestInputData()

	parentVer := int64(301)
	input.BomOutputQty[parentVer] = 1.0
	input.BomItems[parentVer] = []domain.BomItem{
		{ID: 1, BomVersionID: parentVer, VariantID: 801, Quantity: 2.0, ScrapPct: 10.0, QuantityWithScrap: 2.2},
	}

	exploder := domain.NewBomExploder(input, 10)
	subDemands, err := exploder.Explode(parentVer, 10.0, time.Now(), 1)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(subDemands) != 1 {
		t.Fatalf("expected 1 sub-demand, got %d", len(subDemands))
	}
	if subDemands[0].VariantID != 801 || math.Abs(subDemands[0].Quantity-22.0) > 0.001 {
		t.Errorf("expected subdemand qty 22.0, got %f", subDemands[0].Quantity)
	}
}

// 7. BOM kosong / produk tanpa BOM — return slice kosong dan tanpa error/panic
func TestExplode_EmptyBom(t *testing.T) {
	input := createTestInputData()
	nonExistentVer := int64(9999)

	exploder := domain.NewBomExploder(input, 10)
	subDemands, err := exploder.Explode(nonExistentVer, 10.0, time.Now(), 1)

	if err != nil {
		t.Fatalf("expected no error for empty BOM, got: %v", err)
	}

	if len(subDemands) != 0 {
		t.Errorf("expected 0 sub-demands for empty BOM, got %d", len(subDemands))
	}
}

// 8. Phantom Component — skip phantom item, sub-komponennya langsung dibuat
func TestExplode_PhantomComponent(t *testing.T) {
	input := createTestInputData()

	mainVer := int64(401)
	phantomVer := int64(402)

	// Main BOM memiliki Phantom Component (Var 900)
	input.BomOutputQty[mainVer] = 1.0
	input.BomItems[mainVer] = []domain.BomItem{
		{ID: 1, BomVersionID: mainVer, VariantID: 900, IsPhantom: true, QuantityWithScrap: 1.0},
		{ID: 2, BomVersionID: mainVer, VariantID: 901, IsPhantom: false, QuantityWithScrap: 5.0},
	}
	input.ActiveBoms[900] = phantomVer

	// Phantom BOM memiliki Sub-komponen (Var 902 dan 903)
	input.BomOutputQty[phantomVer] = 1.0
	input.BomItems[phantomVer] = []domain.BomItem{
		{ID: 3, BomVersionID: phantomVer, VariantID: 902, IsPhantom: false, QuantityWithScrap: 2.0},
		{ID: 4, BomVersionID: phantomVer, VariantID: 903, IsPhantom: false, QuantityWithScrap: 3.0},
	}

	exploder := domain.NewBomExploder(input, 10)
	subDemands, err := exploder.Explode(mainVer, 10.0, time.Now(), 1)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Var 900 (phantom) TIDAK boleh ada di subDemands!
	// Yang harus ada adalah: 902 (20.0), 903 (30.0), 901 (50.0)
	for _, sd := range subDemands {
		if sd.VariantID == 900 {
			t.Errorf("phantom variant 900 should NOT be included in sub-demands")
		}
	}

	if len(subDemands) != 3 {
		t.Fatalf("expected 3 sub-demands (phantom children + direct component), got %d", len(subDemands))
	}
}
