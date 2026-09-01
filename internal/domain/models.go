// Package domain mendefinisikan tipe data inti dan kontrak bisnis untuk MRP Engine.
// Paket ini murni berisikan logika domain, tanpa dependensi ke database atau redis.
package domain

import "time"

// SupportedSchemaVersion adalah versi kontrak job payload yang didukung service ini.
// Job dengan schemaVersion yang tidak sesuai akan langsung di-reject ke status failed_permanent.
const SupportedSchemaVersion = 1

// RunStatus mendefinisikan status siklus hidup dari kalkulasi mrp_runs.
type RunStatus string

const (
	RunStatusPending         RunStatus = "pending"
	RunStatusRunning         RunStatus = "running"
	RunStatusCompleted       RunStatus = "completed"
	RunStatusFailed          RunStatus = "failed"
	RunStatusFailedPermanent RunStatus = "failed_permanent"
)

// OrderType mendefinisikan tipe planned order yang dihasilkan.
type OrderType string

const (
	OrderTypeProduction OrderType = "production"
	OrderTypePurchase   OrderType = "purchase"
)

// PlannedOrderStatus mendefinisikan status awal planned order saat dibuat.
const PlannedOrderStatusProposed = "proposed"

// MrpJob merepresentasikan payload job sesuai mrp-job-contract.schema.json.
type MrpJob struct {
	SchemaVersion int       `json:"schemaVersion"`
	RunID         int64     `json:"runId"`
	TenantSchema  string    `json:"tenantSchema"`
	PlanFrom      time.Time `json:"planFrom"`
	PlanTo        time.Time `json:"planTo"`
	TriggeredBy   int64     `json:"triggeredBy"`
}

// MrpDemand merepresentasikan baris kebutuhan material dari tabel mrp_demands.
type MrpDemand struct {
	ID          int64     `json:"id"`
	VariantID   int64     `json:"variantId"`
	DemandType  string    `json:"demandType"`
	Quantity    float64   `json:"quantity"`
	UomID       int64     `json:"uomId"`
	NeededDate  time.Time `json:"neededDate"`
	WarehouseID int64     `json:"warehouseId"`
	SoID        *int64    `json:"soId,omitempty"`
	SoItemID    *int64    `json:"soItemId,omitempty"`
	Status      string    `json:"status"`
}

// SubDemand merepresentasikan kebutuhan komponen anak hasil dari BOM explosion.
type SubDemand struct {
	VariantID       int64     `json:"variantId"`
	Quantity        float64   `json:"quantity"`
	UomID           int64     `json:"uomId"`
	NeededDate      time.Time `json:"neededDate"`
	WarehouseID     int64     `json:"warehouseId"`
	ParentVariantID int64     `json:"parentVariantId,omitempty"`
	Depth           int       `json:"depth"`
}

// DemandGroup mengelompokkan beberapa demands yang memiliki variant dan warehouse yang sama.
type DemandGroup struct {
	VariantID          int64       `json:"variantId"`
	WarehouseID        int64       `json:"warehouseId"`
	TotalQuantity      float64     `json:"totalQuantity"`
	Demands            []MrpDemand `json:"demands"`
	EarliestNeededDate time.Time   `json:"earliestNeededDate"`
}

// StockEntry merepresentasikan saldo stok saat ini per variant dan warehouse dari view stock_summary.
type StockEntry struct {
	VariantID      int64   `json:"variantId"`
	WarehouseID    int64   `json:"warehouseId"`
	BatchID        string  `json:"batchId,omitempty"`
	QuantityOnHand float64 `json:"quantityOnHand"`
}

// OnOrderEntry merepresentasikan quantity yang sedang dalam proses pembelian dari PO yang sudah dikonfirmasi.
type OnOrderEntry struct {
	VariantID       int64     `json:"variantId"`
	WarehouseID     int64     `json:"warehouseId"`
	QuantityOnOrder float64   `json:"quantityOnOrder"`
	EarliestArrival time.Time `json:"earliestArrival"`
}

// BomVersion merepresentasikan header versi BOM yang aktif.
type BomVersion struct {
	ID             int64   `json:"id"`
	BomHeaderID    int64   `json:"bomHeaderId"`
	VariantID      int64   `json:"variantId"`
	OutputQuantity float64 `json:"outputQuantity"`
	OutputUomID    int64   `json:"outputUomId"`
	LeadTimeDays   int     `json:"leadTimeDays"`
}

// BomItem merepresentasikan baris rincian komponen pada suatu versi BOM.
type BomItem struct {
	ID                int64    `json:"id"`
	BomVersionID      int64    `json:"bomVersionId"`
	ParentItemID      *int64   `json:"parentItemId,omitempty"`
	VariantID         int64    `json:"variantId"`
	IsPhantom         bool     `json:"isPhantom"`
	Quantity          float64  `json:"quantity"`
	UomID             int64    `json:"uomId"`
	ScrapPct          float64  `json:"scrapPct"`
	QuantityWithScrap float64  `json:"quantityWithScrap"`
	Level             int      `json:"level"`
	Sequence          int      `json:"sequence"`
}

// VariantInfo menyimpan metadata produk variant seperti lead time pembelian dan UoM dasar.
type VariantInfo struct {
	VariantID    int64 `json:"variantId"`
	LeadTimeDays int   `json:"leadTimeDays"`
	BaseUomID    int64 `json:"baseUomId"`
}

// InputData adalah penampung seluruh data input yang dibaca dari database untuk suatu MRP run.
type InputData struct {
	RunID        int64
	TenantSchema string
	PlanFrom     time.Time
	PlanTo       time.Time

	Demands []MrpDemand

	// StockMap: map[variantID][warehouseID] = quantity_on_hand
	StockMap map[int64]map[int64]float64

	// OnOrderMap: map[variantID][warehouseID] = OnOrderEntry
	OnOrderMap map[int64]map[int64]OnOrderEntry

	// ActiveBoms: map[variantID] = bom_version_id
	ActiveBoms map[int64]int64

	// BomItems: map[bomVersionID] = []BomItem
	BomItems map[int64][]BomItem

	// BomOutputQty: map[bomVersionID] = output_quantity
	BomOutputQty map[int64]float64

	// BomLeadTime: map[bomVersionID] = lead_time_days
	BomLeadTime map[int64]int

	// VariantInfo: map[variantID] = VariantInfo
	VariantInfo map[int64]VariantInfo

	// SupplierHistory: map[variantID] = last_supplier_id
	SupplierHistory map[int64]int64
}

// NewInputData menginisialisasi struct InputData dengan map yang sudah teralokasi.
func NewInputData(runID int64, tenantSchema string, planFrom, planTo time.Time) *InputData {
	return &InputData{
		RunID:           runID,
		TenantSchema:    tenantSchema,
		PlanFrom:        planFrom,
		PlanTo:          planTo,
		Demands:         []MrpDemand{},
		StockMap:        make(map[int64]map[int64]float64),
		OnOrderMap:      make(map[int64]map[int64]OnOrderEntry),
		ActiveBoms:      make(map[int64]int64),
		BomItems:        make(map[int64][]BomItem),
		BomOutputQty:    make(map[int64]float64),
		BomLeadTime:     make(map[int64]int),
		VariantInfo:     make(map[int64]VariantInfo),
		SupplierHistory: make(map[int64]int64),
	}
}

// PlannedOrder merepresentasikan rencana pesanan produksi atau pembelian yang dihasilkan engine.
type PlannedOrder struct {
	ID                  int64                `json:"id"`
	MrpRunID            int64                `json:"mrpRunId"`
	VariantID           int64                `json:"variantId"`
	WarehouseID         int64                `json:"warehouseId"`
	OrderType           OrderType            `json:"orderType"`
	Quantity            float64              `json:"quantity"`
	UomID               int64                `json:"uomId"`
	PlannedStart        time.Time            `json:"plannedStart"`
	PlannedFinish       time.Time            `json:"plannedFinish"`
	BomVersionID        *int64               `json:"bomVersionId,omitempty"`
	SuggestedSupplierID *int64               `json:"suggestedSupplierId,omitempty"`
	IsOverdue           bool                 `json:"isOverdue"`
	Status              string               `json:"status"`
	CoveredDemands      []PlannedOrderDemand `json:"coveredDemands,omitempty"`
}

// PlannedOrderDemand merepresentasikan relasi mapping antara planned order dengan demand sumber.
type PlannedOrderDemand struct {
	PlannedOrderID    int64   `json:"plannedOrderId"`
	DemandID          int64   `json:"demandId"`
	QuantityAllocated float64 `json:"quantityAllocated"`
}

// CalcStats menyimpan ringkasan statistik dari hasil kalkulasi MRP.
type CalcStats struct {
	TotalProduction int `json:"totalProduction"`
	TotalPurchase   int `json:"totalPurchase"`
	TotalFulfilled  int `json:"totalFulfilled"`
	OverdueOrders   int `json:"overdueOrders"`
}

// CalcResult adalah paket hasil lengkap kalkulasi MRP yang siap disimpan ke database.
type CalcResult struct {
	PlannedOrders      []PlannedOrder `json:"plannedOrders"`
	FulfilledDemandIDs []int64        `json:"fulfilledDemandIds"`
	PlannedDemandIDs   []int64        `json:"plannedDemandIds"`
	Stats              CalcStats      `json:"stats"`
}
