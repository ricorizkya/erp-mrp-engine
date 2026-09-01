package domain

import "time"

// MrpJob merepresentasikan payload job sesuai mrp-job-contract.schema.json.
// Kalau schema versi berubah, struct ini dan file JSON schema harus
// diupdate bersamaan dengan sisi NestJS.
type MrpJob struct {
	SchemaVersion int       `json:"schemaVersion"`
	RunID         int64     `json:"runId"`
	TenantSchema  string    `json:"tenantSchema"`
	PlanFrom      time.Time `json:"planFrom"`
	PlanTo        time.Time `json:"planTo"`
	TriggeredBy   int64     `json:"triggeredBy"`
}

// SupportedSchemaVersion adalah versi kontrak yang didukung build Go ini.
// Job dengan SchemaVersion selain ini WAJIB di-reject langsung ke
// failed_permanent (skip retry) — lihat spec section 6.2.
const SupportedSchemaVersion = 1

// RunStatus — enum status mrp_runs.status, harus sinkron dengan CHECK
// constraint di 012_mrp_engine_service_support.sql
type RunStatus string

const (
	RunStatusPending         RunStatus = "pending"
	RunStatusRunning         RunStatus = "running"
	RunStatusCompleted       RunStatus = "completed"
	RunStatusFailed          RunStatus = "failed"
	RunStatusFailedPermanent RunStatus = "failed_permanent"
)

// PlannedOrder — hasil MRP run yang akan di-insert ke tabel planned_orders.
// Field mengikuti kolom di 007_create_production_planning.sql.
type PlannedOrder struct {
	VariantID           int64
	OrderType           string // "production" | "purchase"
	Quantity            float64
	UomID               int64
	PlannedStart        time.Time
	PlannedFinish       time.Time
	BomVersionID        *int64
	SuggestedSupplierID *int64
}
