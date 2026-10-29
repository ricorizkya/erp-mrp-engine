# MRP Engine — PRD, System Design & Task List

> **Produk:** ERP Manufaktur SaaS — MRP Engine Service
> **Bahasa:** GoLang 1.22+
> **Status dokumen:** Arsip desain dan task list historis (1 September 2026), bukan status implementasi terkini.
> **Repo:** `erp-mrp-engine` (terpisah dari `erp-backend`)

> **Status implementasi terkini (29 Oktober 2026):** Worker Go, BOM exploder,
> calculator, scheduler, database writer, reconciliation NestJS, dan tagged
> integration tests sudah tersedia. Lihat `README.md` dan
> `docs/mrp-test-validation-report.md` untuk perilaku dan bukti terbaru. Checklist
> historis di bawah tidak boleh dipakai sebagai status terkini.

---

## ⚠️ Ringkasan Rekonsiliasi — Baca Dulu Sebelum Lanjut

Dokumen ini awalnya ditulis sebelum schema database asli (`007_create_production_planning.sql`) dan implementasi NestJS nyata diverifikasi. Beberapa asumsi di draft awal **salah** dan sudah dikoreksi di seluruh dokumen ini:

| Asumsi lama (SALAH) | Kenyataan (dikoreksi) |
|---|---|
| Semua ID (`runId`, `PlannedOrder.ID`, dll) adalah UUID, di-generate sebelum insert | **BIGINT** (`GENERATED ALWAYS AS IDENTITY`) — Go tidak bisa pre-generate ID, harus insert dulu lalu ambil via `RETURNING` |
| Queue name `mrp-jobs`, key Redis `bull:mrp-jobs:wait` | Queue name aktual: **`mrp-calculation`** (dipakai konsisten sebagai nama queue DAN nama job di `.add()`) |
| `opts.attempts: 3` dengan exponential backoff di level BullMQ | **`attempts: 1`** — retry level job dimatikan di BullMQ, sepenuhnya di-handle oleh `MrpReconciliationService` (cron NestJS, cap 3x, cek `job.isActive()` dulu) |
| Status run: `running → completed / failed` | Diperluas: **`pending → running → completed / failed → failed_permanent`** |
| "Hanya satu run boleh berjalan bersamaan per tenant" (FR-01) — tidak ada mekanisme | **Partial unique index** `idx_mrp_runs_one_active_per_tenant` di migration 012, plus try/catch `23505` di aplikasi |
| `jobId` custom BullMQ boleh angka murni | BullMQ menolak `jobId` integer murni — format terkini: `mrp-${tenantCode}-run-${runId}` |
| Tanggal job payload cukup `"2026-01-01"` | Go `time.Time` butuh RFC3339 lengkap — payload pakai `.toISOString()` (`"2026-01-01T00:00:00.000Z"`) |
| GoLang pakai raw `BLPOP`, "kompatibel BullMQ v5" | Klaim ini tidak akurat secara mekanisme (BullMQ pakai Lua script atomic, bukan BLPOP sederhana). **Divalidasi via gobullmq**. Jalur retry dan reconciliation telah divalidasi via runtime test (11 Oktober 2026): deteksi stuck, requeue, idempotensi, dan chaos crash terbukti stabil. |
| Phase 8 (NestJS Side Updates) — belum dikerjakan | **Sudah selesai** di `erp-backend` (`MrpRunService`, `PlannedOrderService`, `MrpReconciliationService`, migration 012, role `mrp_service`). Detail di Section 3, Phase 8 sudah diupdate jadi catatan retrospektif, bukan task aktif. |

**Yang TIDAK berubah dan masih valid:** algoritma BOM explosion (Section 2.8), struktur task Phase 1-7 (project setup, database layer, MRP core logic, testing), skenario integration test, target performa.

---


## Daftar Isi

1. [Product Requirements Document (PRD)](#1-product-requirements-document-prd)
2. [System Design](#2-system-design)
3. [Task List](#3-task-list)

---

# 1. Product Requirements Document (PRD)

## 1.1 Latar Belakang & Masalah

### Konteks Bisnis

ERP Manufaktur SaaS yang sedang dibangun melayani industri tekstil, garmen, dan sejenisnya. Salah satu pain point terbesar manufaktur UKM adalah **perencanaan material yang masih manual** — production planner harus menghitung sendiri:

- Berapa bahan baku yang harus dipesan minggu ini?
- Kapan harus mulai produksi supaya order selesai tepat waktu?
- Apakah stok yang ada cukup untuk memenuhi semua order yang masuk?

Proses manual ini memakan waktu berjam-jam, rawan error, dan sering menghasilkan dua masalah berlawanan secara bersamaan: **overstock** (terlalu banyak beli) dan **stockout** (kehabisan bahan di tengah produksi).

### Solusi yang Dibangun

MRP (Material Requirements Planning) Engine adalah service yang secara otomatis menjawab ketiga pertanyaan di atas. Berdasarkan demand yang masuk (dari Sales Order, forecast, atau safety stock), engine menghitung:

1. **Apa** yang perlu diproduksi atau dibeli
2. **Berapa banyak** — setelah dikurangi stok yang ada dan pembelian yang sedang on-order
3. **Kapan** — dengan menghitung mundur dari tanggal dibutuhkan berdasarkan lead time

Output adalah **Planned Orders** yang bisa di-review dan di-approve oleh planner, lalu otomatis dikonversi menjadi Work Order (untuk produksi) atau Purchase Order draft (untuk pembelian).

---

## 1.2 Target Pengguna

| Persona | Peran | Kebutuhan Utama |
|---|---|---|
| Production Planner | Merencanakan jadwal produksi | Tahu apa yang harus diproduksi dan kapan |
| Procurement Manager | Mengelola pembelian bahan baku | Tahu apa yang harus dibeli dan kapan |
| Plant Manager | Oversight operasional pabrik | Dashboard status produksi vs demand |
| Direktur | Pengambil keputusan strategis | Visibilitas overall kapasitas dan order fulfillment |

---

## 1.3 Goals & Non-Goals

### Goals (v1)

- ✅ Hitung net requirement secara otomatis dari semua open demand
- ✅ Explode BOM multi-level (termasuk phantom components)
- ✅ Generate planned production orders dengan tanggal terkalkulasi
- ✅ Generate planned purchase orders dengan supplier suggestion
- ✅ Backward scheduling dari needed date berdasarkan lead time
- ✅ Proses ratusan demand dalam waktu < 30 detik
- ✅ Tidak mengganggu NestJS backend (async via queue)
- ✅ Idempotent: re-run MRP menghasilkan hasil yang konsisten

### Non-Goals (defer ke v2)

- ❌ Lot sizing (Economic Order Quantity, Min-Max)
- ❌ Capacity planning (jam mesin, jam tenaga kerja)
- ❌ Multi-plant / multi-warehouse routing
- ❌ Forecast smoothing / statistical forecasting
- ❌ Real-time MRP (event-driven per transaksi)
- ❌ What-if simulation
- ❌ Finite scheduling

---

## 1.4 Functional Requirements

### FR-01: Trigger MRP Run

**Deskripsi:** Planner dapat memulai MRP calculation dari UI (via NestJS API). MRP berjalan secara asynchronous — planner tidak perlu menunggu.

**Acceptance Criteria:**
- Planner dapat memilih planning horizon (date range)
- System memberikan `run_id` (BIGINT, hasil `RETURNING id` dari insert `mrp_runs`) segera setelah trigger
- Hanya satu MRP run yang boleh berjalan bersamaan per tenant — di-enforce via **partial unique index** `idx_mrp_runs_one_active_per_tenant` di level database (`WHERE status IN ('pending','running')`), plus cek di level aplikasi (`triggerRun()`) untuk pesan error yang jelas. Index adalah pertahanan terakhir terhadap race condition; aplikasi WAJIB catch `unique_violation` (kode `23505`) secara eksplisit
- Status run: `pending` (row dibuat, job di-push) → `running` (Go pickup) → `completed` atau `failed` → `failed_permanent` (setelah 3x retry gagal via reconciliation, lihat FR-09)

### FR-02: Load Input Data

**Deskripsi:** Engine memuat semua data yang dibutuhkan dari PostgreSQL sebelum kalkulasi dimulai.

**Acceptance Criteria:**
- Memuat semua `mrp_demands` dengan status `open` atau `planned` dalam planning horizon
- Memuat `stock_summary` (materialized view) untuk semua variant yang punya demand
- Memuat quantity on-order dari `purchase_order_items` (PO confirmed, belum fully received)
- Memuat BOM aktif (`bom_versions` status `active`) untuk semua variant yang dibutuhkan
- Memuat semua `bom_items` yang relevan sekaligus (single query, bukan N+1)
- Semua data dimuat dalam satu batch untuk minimasi DB roundtrips

### FR-03: Net Requirement Calculation

**Deskripsi:** Engine menghitung kebutuhan bersih setelah memperhitungkan stok yang ada.

**Formula:**
```
Net Requirement = Gross Requirement - Projected On-Hand - Scheduled Receipts

Dimana:
  Gross Requirement   = SUM(demand.quantity) per (variant_id, warehouse_id)
  Projected On-Hand   = stock_summary.quantity_on_hand
  Scheduled Receipts  = SUM(po_items.quantity_pending) dari PO confirmed
```

**Acceptance Criteria:**
- Jika Net Requirement ≤ 0: tidak ada planned order, demand di-mark fulfilled
- Jika Net Requirement > 0: lanjut ke FR-04 atau FR-05
- Perhitungan dilakukan per kombinasi (variant_id, warehouse_id)
- Toleransi floating point: selisih < 0.001 dianggap nol

### FR-04: Planned Production Order Generation

**Deskripsi:** Jika variant punya BOM aktif, engine membuat planned production order.

**Acceptance Criteria:**
- Dibuat jika: variant punya `bom_versions` dengan status `active`
- Quantity = Net Requirement
- `planned_finish` = tanggal paling awal dari demands yang di-cover
- `planned_start` = `planned_finish` - `lead_time_days` (dari durasi operasi BOM atau `product_variants.lead_time_days`)
- Jika `planned_start` < today: set = today, tandai sebagai **urgent/overdue**
- BOM version yang dipakai dicatat di `planned_orders.bom_version_id`

### FR-05: Planned Purchase Order Generation

**Deskripsi:** Jika variant tidak punya BOM aktif (raw material), engine membuat planned purchase order.

**Acceptance Criteria:**
- Dibuat jika: variant tidak punya BOM aktif
- Quantity = Net Requirement
- `suggested_supplier_id` diisi dari PO history (supplier terakhir untuk variant ini)
- Jika tidak ada history: `suggested_supplier_id` = NULL (user pilih manual)
- `planned_finish` = tanggal paling awal dari demands yang di-cover
- `planned_start` = `planned_finish` - `product_variants.lead_time_days`

### FR-06: Multi-Level BOM Explosion

**Deskripsi:** Untuk setiap planned production order, engine meng-explode BOM secara recursive untuk mengidentifikasi kebutuhan komponen di semua level.

**Acceptance Criteria:**
- Explode semua level sampai raw material (tidak punya BOM aktif)
- Hitung quantity yang dibutuhkan: `parent_qty × (item.quantity_with_scrap / bom.output_quantity)`
- Handle `is_phantom = true`: skip item, langsung explode sub-BOM-nya
- Proteksi circular dependency: max depth = 10 level (configurable), error jika terlampaui
- Sub-demands dari BOM explosion diproses dengan logika yang sama (recursive MRP)

### FR-07: Lead Time Scheduling

**Deskripsi:** Engine menghitung `planned_start` berdasarkan `planned_finish` dan lead time.

**Acceptance Criteria:**
- **Produksi:** lead time = SUM(bom_operations.duration_minutes) / 480 (8 jam/hari), minimum 1 hari
- **Pembelian:** lead time = `product_variants.lead_time_days`, default 7 jika NULL
- Scheduling mode: **backward** (dari `needed_date` mundur ke `planned_start`)
- Tidak menghitung hari kalender (v1 mengabaikan weekend dan holiday)
- Jika `planned_start` < today: gunakan today, tambahkan flag `is_overdue = true`

### FR-08: Save Results

**Deskripsi:** Engine menyimpan semua planned orders ke database dalam satu transaksi.

**Acceptance Criteria:**
- Insert planned orders dalam batch (bukan satu per satu)
- Insert `planned_order_demands` mapping (planned order ↔ demand sumber)
- Update status `mrp_demands` yang ter-cover menjadi `planned`
- Update `mrp_runs` dengan: status, total_planned_production, total_planned_purchase, duration_ms
- Jika terjadi error: rollback semua, update `mrp_runs.status = 'failed'` dengan error message
- Idempotent: re-run MRP untuk period yang sama menghasilkan planned orders baru (yang lama tetap ada dengan status `cancelled` jika belum di-approve)

### FR-09: Error Handling & Recovery

**Deskripsi:** Engine menangani error dengan graceful dan tidak merusak data yang sudah ada.

**⚠️ Koreksi penting:** ada DUA mekanisme retry yang berbeda level, jangan tertukar:
1. **DB connection retry** (internal Go, dalam satu job run) — retry koneksi yang putus di tengah kalkulasi. Ini yang dimaksud `MRP_RETRY_ATTEMPTS=3` di config (Section 2.10).
2. **Job-level retry** (kalau seluruh job gagal/stuck) — BUKAN ditangani BullMQ `attempts` (sengaja di-set `attempts: 1`, dimatikan). Ditangani `MrpReconciliationService` di NestJS: cron tiap 1 menit, cek run `status='running'` dengan `started_at` > 5 menit lalu, cek `job.isActive()` di BullMQ dulu (kalau masih aktif, skip — proses mungkin masih hidup wajar), baru retry dengan `runId` sama (via `job.remove()` dulu sebelum push ulang — job lama tidak dihapus otomatis karena `removeOnComplete/removeOnFail: false` untuk audit trail), cap 3x, lalu `failed_permanent`. **Ini didesain dan diimplementasi di sisi NestJS (`erp-backend`), bukan di Go.**

**Acceptance Criteria:**
- Database connection error (dalam satu run): retry 3x dengan exponential backoff (level Go, internal)
- BOM circular dependency: error dengan pesan yang jelas (variant mana yang bermasalah)
- Missing data (BOM items kosong, UoM tidak ada): log warning, skip variant, lanjutkan
- Timeout (> 5 menit): job dianggap stuck — DIDETEKSI dan DI-RETRY oleh reconciliation NestJS (bukan oleh Go sendiri, Go tidak tahu dirinya "stuck")
- Semua error di-log dengan structured logging (JSON format)
- Job-level retry: lihat mekanisme reconciliation di atas, BUKAN BullMQ `attempts`

### FR-10: Status Monitoring

**Deskripsi:** NestJS dapat memantau status MRP run dan mengambil hasilnya.

**Acceptance Criteria:**
- GoLang update `mrp_runs.status` saat: mulai (`running`, hanya jika WHERE status='pending' berhasil match — defense terhadap race condition), selesai (`completed`), gagal (`failed`)
- `failed_permanent` di-set oleh reconciliation NestJS (bukan Go) setelah 3x retry habis, atau oleh Go langsung kalau `schemaVersion` job tidak dikenali (skip retry, karena version mismatch bukan transient error)
- NestJS polling `mrp_runs` table untuk status (tidak butuh real-time push)
- Setelah `completed`: NestJS bisa query `planned_orders` berdasarkan `mrp_run_id`

---

## 1.5 Non-Functional Requirements

### Performance

| Metrik | Target v1 |
|---|---|
| Processing time untuk 100 demands | < 10 detik |
| Processing time untuk 500 demands | < 30 detik |
| Memory usage per run | < 512 MB |
| Database queries per run | < 20 queries (batch loading) |
| Concurrent runs per instance | 3 (configurable) |

### Reliability

| Metrik | Target |
|---|---|
| Availability | 99.5% (downtime < 3.6 jam/bulan) |
| Job retry on failure | 3x dengan exponential backoff |
| Data consistency | Semua writes dalam single transaction |
| Recovery dari crash | Job re-queued otomatis oleh BullMQ |

### Security

- Service tidak expose HTTP endpoint publik
- Komunikasi hanya via Redis queue (internal network)
- Database credentials via environment variables (tidak hardcode)
- Tenant isolation: setiap job menggunakan schema-specific connection
- Tidak ada cross-tenant data leakage (PostgreSQL search_path per job)

### Observability

- Structured logging (JSON) ke stdout
- Log level: DEBUG / INFO / WARN / ERROR (configurable via env)
- Setiap MRP run log: tenant, duration, jumlah demands, jumlah planned orders
- Error log include: stack trace, tenant, run_id, error message

---

## 1.6 Constraints & Assumptions

### Constraints

1. **Database:** PostgreSQL 16, schema per tenant (`tenant_{code}`)
2. **Queue:** Redis 7+ dengan format BullMQ v5 yang kompatibel dengan NestJS
3. **GoLang version:** 1.22 minimum (untuk range-over-func)
4. **Server:** VPS lokal (Biznet/IDCloudHost), tidak ada managed Kubernetes
5. **Budget infrastruktur:** GoLang service berjalan di VPS yang sama dengan NestJS (tidak ada server terpisah untuk v1)

### Assumptions

1. BOM version yang dipakai selalu yang berstatus `active` saat MRP dijalankan
2. Stock summary (`stock_summary` materialized view) sudah up-to-date sebelum MRP dijalankan
3. Satu variant hanya boleh punya satu BOM header dengan satu version `active` (enforced by DB constraint)
4. Lead time tidak memperhitungkan hari libur atau kapasitas produksi (v1)
5. Satu MRP run hanya berjalan per tenant (bukan per warehouse atau per product group)

---

# 2. System Design

## 2.1 High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                        VPS (Single Server)                          │
│                                                                     │
│  ┌──────────────────┐    ┌─────────┐    ┌───────────────────────┐  │
│  │   NestJS Backend  │    │  Redis  │    │  GoLang MRP Engine    │  │
│  │                  │───►│  Queue  │───►│                       │  │
│  │  POST /mrp/run   │    │ (BullMQ)│    │  Queue Consumer       │  │
│  │  GET  /mrp/runs  │    │         │    │  MRP Calculator       │  │
│  │                  │◄───│  PubSub │◄───│  BOM Exploder         │  │
│  └────────┬─────────┘    └─────────┘    └──────────┬────────────┘  │
│           │                                         │               │
│           │              ┌──────────────────────────┘               │
│           ▼              ▼                                          │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                    PostgreSQL 16                            │   │
│  │                                                             │   │
│  │  public schema:        tenant_abc schema:                   │   │
│  │  - tenants             - mrp_demands      (INPUT)          │   │
│  │  - users               - stock_summary    (INPUT, MV)      │   │
│  │  - mrp_runs            - purchase_orders  (INPUT)          │   │
│  │                        - bom_versions     (INPUT)          │   │
│  │                        - bom_items        (INPUT)          │   │
│  │                        - planned_orders   (OUTPUT)         │   │
│  │                        - work_orders      (OUTPUT, draft)  │   │
│  └─────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

## 2.2 Service Boundary

GoLang MRP Engine adalah **stateless worker** yang:
- Menerima job dari Redis queue
- Membaca data dari PostgreSQL
- Menulis hasil ke PostgreSQL
- Tidak expose HTTP endpoint
- Tidak menyimpan state di memory antar job

NestJS tetap menjadi **single source of truth** untuk semua business logic lainnya. GoLang hanya bertanggung jawab atas kalkulasi MRP.

---

## 2.3 Data Flow Detail

```
                         ┌─────────────────────────────────┐
                         │         MRP Job (Redis)          │
                         │  {                               │
                         │    runId: "uuid",                │
                         │    tenantSchema: "tenant_abc",   │
                         │    planFrom: "2026-01-01",       │
                         │    planTo:   "2026-03-31",       │
                         │    triggeredBy: "user-uuid"      │
                         │  }                               │
                         └───────────────┬─────────────────┘
                                         │
                                         ▼
                         ┌─────────────────────────────────┐
                         │         Phase 1: LOAD            │
                         │                                 │
                         │  Query 1: open demands          │
                         │  Query 2: stock_summary         │
                         │  Query 3: on_order quantities   │
                         │  Query 4: active BOMs           │
                         │  Query 5: all BOM items         │
                         │  Query 6: supplier history      │
                         │                                 │
                         │  Result: InputData struct        │
                         └───────────────┬─────────────────┘
                                         │
                                         ▼
                         ┌─────────────────────────────────┐
                         │       Phase 2: CALCULATE         │
                         │                                 │
                         │  Group demands by               │
                         │  (variant_id, warehouse_id)     │
                         │           │                     │
                         │           ▼                     │
                         │  Per group:                     │
                         │  net_req = gross                │
                         │          - on_hand              │
                         │          - on_order             │
                         │           │                     │
                         │    ┌──────┴──────┐             │
                         │    ▼             ▼             │
                         │  has BOM?     no BOM?          │
                         │    │             │             │
                         │    ▼             ▼             │
                         │  Planned      Planned          │
                         │  Production   Purchase         │
                         │    │                           │
                         │    ▼                           │
                         │  Explode BOM                   │
                         │  (recursive)                   │
                         │  → sub-demands                 │
                         │  → repeat loop                 │
                         └───────────────┬─────────────────┘
                                         │
                                         ▼
                         ┌─────────────────────────────────┐
                         │         Phase 3: WRITE           │
                         │                                 │
                         │  BEGIN TRANSACTION              │
                         │  Bulk INSERT planned_orders     │
                         │  Bulk INSERT planned_order_     │
                         │           demands               │
                         │  Bulk UPDATE mrp_demands        │
                         │           (status=planned)      │
                         │  UPDATE mrp_runs (completed)    │
                         │  COMMIT                         │
                         └─────────────────────────────────┘
```

---

## 2.4 Project Structure

```
erp-mrp-engine/
│
├── main.go                    ← Entry point: init config, DB, Redis, start worker
├── go.mod
├── go.sum
├── Dockerfile
├── .env.example
├── README.md
│
├── config/
│   └── config.go              ← Load env vars, validation
│
├── db/
│   ├── pool.go                ← pgx connection pool initialization
│   ├── tenant.go              ← Tenant-aware connection (SET search_path)
│   └── queries/
│       ├── demands.go         ← SQL: load open demands
│       ├── stock.go           ← SQL: load stock_summary
│       ├── on_order.go        ← SQL: load PO on-order quantities
│       ├── bom.go             ← SQL: load active BOMs + items
│       ├── suppliers.go       ← SQL: load supplier history
│       └── writer.go          ← SQL: insert planned orders, update statuses
│
├── queue/
│   ├── consumer.go            ← Redis BLPOP consumer (BullMQ compatible)
│   └── models.go              ← Job struct definitions
│
├── mrp/
│   ├── engine.go              ← Orchestrator: coordinate load → calc → write
│   ├── loader.go              ← Aggregate all DB reads into InputData
│   ├── calculator.go          ← Net requirement calculation, group demands
│   ├── bom_exploder.go        ← Recursive BOM explosion + phantom handling
│   ├── scheduler.go           ← Lead time calculation, backward scheduling
│   ├── supplier.go            ← Suggested supplier logic
│   └── writer.go              ← Aggregate all DB writes from CalcResult
│
├── models/
│   ├── demand.go              ← MrpDemand, DemandGroup structs
│   ├── stock.go               ← StockEntry, OnOrderEntry structs
│   ├── bom.go                 ← BomVersion, BomItem, MaterialRequirement structs
│   ├── planned_order.go       ← PlannedOrder, PlannedOrderDemand structs
│   ├── input_data.go          ← InputData (semua data yang diload)
│   └── calc_result.go         ← CalcResult (output kalkulasi)
│
└── internal/
    ├── logger/
    │   └── logger.go          ← Structured JSON logging wrapper
    └── utils/
        └── date.go            ← Date calculation helpers
```

---

## 2.5 Core Data Structures

### InputData

```go
// models/input_data.go
//
// KOREKSI: RunID dan seluruh ID (variant, warehouse, bom version, dll)
// adalah int64 (BIGINT dari Postgres), BUKAN string/UUID. Map keys yang
// tadinya string (misal map[string]...) tetap bisa pakai int64 sebagai
// key langsung — TIDAK perlu di-format jadi string dulu.

type InputData struct {
    RunID       int64
    TenantCode  string
    PlanFrom    time.Time
    PlanTo      time.Time

    Demands     []MrpDemand

    // map[variantID][warehouseID] = qty_on_hand
    StockMap    map[int64]map[int64]float64

    // map[variantID][warehouseID] = {qty, arrival_date}
    OnOrderMap  map[int64]map[int64]OnOrderEntry

    // map[variantID] = bom_version_id
    ActiveBoms  map[int64]int64

    // map[bomVersionID] = []BomItem
    BomItems    map[int64][]BomItem

    // map[bomVersionID] = output_quantity
    BomOutputQty map[int64]float64

    // map[bomVersionID] = lead_time_days (dari operations)
    BomLeadTime map[int64]int

    // map[variantID] = {lead_time_days, supplier_id}
    VariantInfo map[int64]VariantInfo

    // map[variantID] = supplier_id (dari PO history)
    SupplierHistory map[int64]int64
}
```

### CalcResult

```go
// models/calc_result.go

type CalcResult struct {
    PlannedOrders []PlannedOrder
    FulfilledDemandIDs []string  // demands yang tidak perlu planned order
    PlannedDemandIDs   []string  // demands yang di-cover planned order
    Stats CalcStats
}

type CalcStats struct {
    TotalProduction int
    TotalPurchase   int
    TotalFulfilled  int
    OverdueOrders   int
}
```

### PlannedOrder

```go
// models/planned_order.go
//
// KOREKSI PENTING: ID BUKAN pre-generated di Go. planned_orders.id
// adalah BIGINT GENERATED ALWAYS AS IDENTITY — Postgres yang assign
// nilainya SAAT insert. Field ID di bawah kosong (0) sebelum insert,
// diisi dari hasil RETURNING id setelah bulk insert (Query 8 di
// Section 2.7). Ini mengubah cara Writer (Task 3.5) bekerja: tidak
// bisa insert planned_order_demands (Query 9) dalam batch yang SAMA
// dengan insert planned_orders, karena butuh ID hasil insert pertama
// dulu — dua langkah berurutan dalam satu transaksi, bukan dua UNNEST
// paralel independen seperti draft awal.

type PlannedOrder struct {
    ID                  int64     // kosong sebelum insert, diisi dari RETURNING id
    MrpRunID            int64
    VariantID           int64
    OrderType           string    // "production" | "purchase"
    Quantity            float64
    UomID               int64
    PlannedStart        time.Time
    PlannedFinish       time.Time
    BomVersionID        *int64    // nullable, hanya untuk production
    SuggestedSupplierID *int64    // nullable, hanya untuk purchase
    IsOverdue           bool
    Status              string    // "proposed"

    // Demands yang di-cover oleh planned order ini
    CoveredDemands []PlannedOrderDemand
}

type PlannedOrderDemand struct {
    PlannedOrderID    int64  // diisi SETELAH PlannedOrder.ID didapat dari RETURNING
    DemandID          int64
    QuantityAllocated float64
}
```

---

## 2.6 Queue Contract

Format job yang dikirim NestJS dan dibaca GoLang. **Ini adalah interface contract antara dua service — sudah divalidasi via spike test end-to-end (1 September 2026), bukan lagi draft.**

### Job Payload (NestJS → Redis → GoLang)

```json
{
  "id": "mrp-run-3",
  "name": "mrp-calculation",
  "data": {
    "schemaVersion": 1,
    "runId": 3,
    "tenantSchema": "tenant_companyabc",
    "planFrom": "2026-01-01T00:00:00.000Z",
    "planTo":   "2026-03-31T00:00:00.000Z",
    "triggeredBy": 7
  },
  "opts": {
    "jobId": "mrp-run-3",
    "attempts": 1,
    "removeOnComplete": false,
    "removeOnFail": false
  }
}
```

**Perbedaan dari draft awal:**
- `runId` dan `triggeredBy`: **integer (BIGINT)**, bukan UUID string
- `schemaVersion` ditambahkan — kalau Go terima versi yang tidak dikenali, **reject + `failed_permanent` langsung, skip retry** (version mismatch bukan transient error)
- `planFrom`/`planTo`: **RFC3339 lengkap** (`.toISOString()` di NestJS) — Go `time.Time` standar butuh format ini, tanggal pendek (`"2026-01-01"` saja) gagal di-unmarshal
- `jobId` custom memakai format tenant-scoped `mrp-${tenantCode}-run-${runId}` agar tidak collision antar schema tenant
- `opts.attempts: 1` (bukan 3 dengan exponential backoff) — retry level job dimatikan di BullMQ, di-handle reconciliation NestJS (lihat FR-09)
- `removeOnComplete/removeOnFail: false` — job lama TIDAK dihapus otomatis. Ini penting untuk retry: reconciliation WAJIB panggil `job.remove()` sebelum push ulang dengan `jobId` yang sama, kalau tidak BullMQ dedupe dan retry jadi silent no-op

### Queue Names

| Queue | Nama Aktual | Keterangan |
|---|---|---|
| Queue name (NestJS `registerQueue` & Go consumer) | **`mrp-calculation`** | BUKAN `mrp-jobs` seperti draft awal — nama ini dipakai konsisten sebagai queue name DAN sebagai job name di `.add('mrp-calculation', ...)` |
| Job waiting | `bull:mrp-calculation:wait` | |
| Job active | `bull:mrp-calculation:active` | |
| Job completed | `bull:mrp-calculation:completed` | Tetap tersimpan (removeOnComplete: false) |
| Job failed | `bull:mrp-calculation:failed` | Tetap tersimpan (removeOnFail: false) |

### Catatan Kompatibilitas Go ↔ BullMQ

Draft awal mengklaim GoLang bisa pakai raw `BLPOP` yang "kompatibel dengan BullMQ v5" — **klaim ini tidak akurat secara mekanisme**. BullMQ memakai Lua script atomic (`moveToActive`) untuk job locking, bukan `BLPOP` sederhana; implementasi naif dengan `BLPOP` tidak akan berpartisipasi dengan benar di state machine BullMQ (job tidak akan ter-lock dengan benar, `job.isActive()` dari sisi Node tidak akan akurat).

**Solusi yang divalidasi:** library komunitas `go.codycody31.dev/gobullmq` (target asli BullMQ v4.12.2, bukan v5) dipakai sebagai consumer. Validasi pengujian runtime (11 Oktober 2026) mengonfirmasi:
- Job berhasil di-consume dan status transition (`pending → running → completed`) berjalan benar pada happy path.
- **Jalur retry & reconciliation telah tervalidasi:** Skenario B-1a dan B-1b membuktikan deteksi stuck dan re-queue sukses via `job.remove()`. Idempotensi saat re-run dijamin oleh pembersihan planned orders lama per `run_id` sebelum bulk insert (`DeletePlannedOrdersByRunID`).
- **Chaos Worker Crash (B-3):** Membuktikan bahwa saat worker mati, job tertahan di `active` dan reconciliation sengaja men-skip run. Sistem pulih otomatis setelah worker dihidupkan kembali. **Prasyarat Deploy:** Worker wajib disupervisi oleh proses supervisor (systemd `Restart=always` / container restart policy) dan dilengkapi alerting untuk run running > 10 menit.

---

## 2.7 Database Queries

### Query 1: Load Open Demands

```sql
-- db/queries/demands.go

SELECT
    id,
    variant_id,
    demand_type,
    quantity,
    uom_id,
    needed_date,
    warehouse_id,
    so_id,
    so_item_id,
    status
FROM mrp_demands
WHERE status IN ('open', 'planned')
  AND needed_date BETWEEN $1 AND $2
ORDER BY needed_date ASC;
```

### Query 2: Load Stock Summary

```sql
-- db/queries/stock.go

SELECT
    variant_id,
    warehouse_id,
    COALESCE(batch_id::text, '') AS batch_id,
    quantity_on_hand
FROM stock_summary
WHERE quantity_on_hand > 0
  AND variant_id IN (
    SELECT DISTINCT variant_id FROM mrp_demands
    WHERE status IN ('open', 'planned')
      AND needed_date BETWEEN $1 AND $2
  );
```

### Query 3: Load On-Order Quantities

```sql
-- db/queries/on_order.go

SELECT
    poi.variant_id,
    po.warehouse_id,
    SUM(poi.quantity_pending) AS quantity_on_order,
    MIN(po.expected_date)     AS earliest_arrival
FROM purchase_order_items poi
JOIN purchase_orders po ON po.id = poi.po_id
WHERE po.status IN ('confirmed', 'partial')
  AND poi.quantity_pending > 0
  AND poi.variant_id IN (
    SELECT DISTINCT variant_id FROM mrp_demands
    WHERE status IN ('open', 'planned')
      AND needed_date BETWEEN $1 AND $2
  )
GROUP BY poi.variant_id, po.warehouse_id;
```

### Query 4: Load Active BOMs

```sql
-- db/queries/bom.go

SELECT
    bh.variant_id,
    bv.id           AS bom_version_id,
    bv.output_quantity,
    bv.output_uom_id,
    COALESCE(
        (SELECT SUM(duration_minutes) / 480.0
         FROM bom_operations
         WHERE bom_version_id = bv.id),
        1
    )::INTEGER      AS lead_time_days
FROM bom_headers bh
JOIN bom_versions bv ON bv.bom_header_id = bh.id
WHERE bv.status = 'active'
  AND bh.is_active = true;
-- (Tidak di-filter per variant — load semua BOM aktif sekaligus
--  untuk BOM explosion multi-level yang bisa menyentuh variant lain)
```

### Query 5: Load All BOM Items

```sql
-- db/queries/bom.go (lanjutan)

SELECT
    id,
    bom_version_id,
    parent_item_id,
    variant_id,
    is_phantom,
    quantity,
    uom_id,
    scrap_pct,
    quantity_with_scrap,
    level,
    sequence
FROM bom_items
WHERE bom_version_id IN (
    SELECT id FROM bom_versions WHERE status = 'active'
)
ORDER BY bom_version_id, level, sequence;
```

### Query 6: Load Supplier History

```sql
-- db/queries/suppliers.go

SELECT DISTINCT ON (poi.variant_id)
    poi.variant_id,
    po.supplier_id,
    po.po_date
FROM purchase_order_items poi
JOIN purchase_orders po ON po.id = poi.po_id
WHERE po.status NOT IN ('cancelled')
ORDER BY poi.variant_id, po.po_date DESC;
```

### Query 7: Load Variant Info (lead time, dll)

```sql
-- db/queries/demands.go (lanjutan)
--
-- KOREKSI: default lead_time_days sudah di-set di level KOLOM (migration
-- 012: NOT NULL DEFAULT 7), bukan cuma fallback di query. COALESCE di
-- bawah sebenarnya jadi dead code (kolom tidak akan pernah NULL) tapi
-- dipertahankan sebagai defense-in-depth kalau ada baris lama sebelum
-- migration 012 yang somehow masih NULL.

SELECT
    id,
    COALESCE(lead_time_days, 7) AS lead_time_days,
    base_uom_id
FROM product_variants
WHERE id IN (
    SELECT DISTINCT variant_id FROM mrp_demands
    WHERE status IN ('open', 'planned')
      AND needed_date BETWEEN $1 AND $2
);
```

### Query 8: Bulk Insert Planned Orders

```sql
-- db/queries/writer.go
--
-- KOREKSI PENTING: id adalah BIGINT GENERATED ALWAYS AS IDENTITY —
-- TIDAK di-supply di kolom INSERT (hapus dari daftar kolom dan dari
-- UNNEST). WAJIB pakai RETURNING id supaya Go tahu ID yang di-assign
-- Postgres, karena Query 9 (planned_order_demands) butuh ID ini.
--
-- Semua cast ::uuid[] diganti ::bigint[]. mrp_run_id, variant_id,
-- uom_id, bom_version_id, suggested_supplier_id — semua BIGINT sesuai
-- 007_create_production_planning.sql.

INSERT INTO planned_orders (
    mrp_run_id, variant_id, order_type,
    quantity, uom_id,
    planned_start, planned_finish,
    bom_version_id, suggested_supplier_id,
    status, created_at, updated_at
)
SELECT * FROM UNNEST(
    $1::bigint[], $2::bigint[], $3::text[],
    $4::numeric[], $5::bigint[],
    $6::date[], $7::date[],
    $8::bigint[], $9::bigint[],
    $10::text[], $11::timestamptz[], $12::timestamptz[]
)
RETURNING id;
-- Urutan RETURNING id mengikuti urutan input UNNEST — Go harus
-- menyimpan array PlannedOrder dalam urutan yang SAMA persis dengan
-- array yang dikirim ke UNNEST, supaya index hasil RETURNING bisa
-- di-map balik ke PlannedOrderDemand yang bersangkutan.
```

### Query 9: Bulk Insert Planned Order Demands

```sql
-- db/queries/writer.go
--
-- Dijalankan SETELAH Query 8 selesai dan ID hasil RETURNING sudah
-- di-assign ke masing-masing PlannedOrderDemand.PlannedOrderID di Go.
-- Dalam transaksi yang SAMA dengan Query 8 (bukan dua transaksi
-- terpisah — commit/rollback bersama).

INSERT INTO planned_order_demands (
    planned_order_id, demand_id, quantity_allocated
)
SELECT * FROM UNNEST(
    $1::bigint[], $2::bigint[], $3::numeric[]
);
```

### Query 10: Update Demand Statuses

```sql
-- db/queries/writer.go

UPDATE mrp_demands
SET status = 'planned', updated_at = NOW()
WHERE id = ANY($1::bigint[]);
```

---

## 2.8 BOM Explosion Algorithm Detail

```
FUNCTION explode_bom(
    bom_version_id : string,
    parent_quantity: float64,      -- qty produk induk yang dibutuhkan
    needed_date    : time.Time,
    warehouse_id   : string,
    depth          : int,
    visited        : map[string]bool,
    input          : InputData,
    sub_demands    : *[]SubDemand  -- output: sub-demands yang dihasilkan
):

    IF depth > MAX_BOM_DEPTH:
        RETURN error("BOM depth exceeded at version " + bom_version_id)

    IF visited[bom_version_id]:
        RETURN error("Circular BOM detected at version " + bom_version_id)
    visited[bom_version_id] = true

    bom_output_qty = input.BomOutputQty[bom_version_id]
    ratio          = parent_quantity / bom_output_qty

    // Ambil hanya items level 1 (parent_item_id IS NULL)
    top_level_items = filter(input.BomItems[bom_version_id], item.ParentItemID == nil)

    FOR EACH item IN top_level_items:
        required_qty = ratio × item.QuantityWithScrap

        IF item.IsPhantom:
            // Cek apakah phantom item punya BOM sendiri
            sub_bom_id, has_bom = input.ActiveBoms[item.VariantID]
            IF has_bom:
                // Explode sub-BOM secara recursive tanpa membuat sub-demand
                explode_bom(
                    sub_bom_id, required_qty, needed_date,
                    warehouse_id, depth+1, visited, input, sub_demands
                )
            // Jika phantom tapi tidak punya BOM: abaikan (data anomaly, log warning)
            CONTINUE

        // Non-phantom item: buat sub-demand
        sub_demand = SubDemand{
            VariantID:   item.VariantID,
            Quantity:    required_qty,
            UomID:       item.UomID,
            NeededDate:  needed_date,  // needed SEBELUM produksi induk dimulai
            WarehouseID: warehouse_id,
        }
        APPEND sub_demands ← sub_demand

        // Cek apakah komponen ini juga punya BOM (semi-finished)
        sub_bom_id, has_sub_bom = input.ActiveBoms[item.VariantID]
        IF has_sub_bom:
            // Explode lebih lanjut untuk mendapatkan raw material
            explode_bom(
                sub_bom_id, required_qty, needed_date,
                warehouse_id, depth+1, visited, input, sub_demands
            )

    DELETE visited[bom_version_id]  // allow revisit dari path berbeda
```

---

## 2.9 Error Handling Strategy

```
Error Type               | Handling
─────────────────────────┼──────────────────────────────────────────
DB connection failed     | Retry 3x (exponential backoff: 1s, 2s, 4s)
                         | Setelah 3x gagal: update mrp_runs = 'failed'
                         |
Circular BOM detected    | Log error dengan detail variant
                         | Skip variant tersebut, lanjutkan yang lain
                         | Catat di mrp_runs.error_message
                         |
BOM items kosong         | Log warning, treat sebagai "no BOM"
                         | Buat planned purchase order
                         |
Stock data stale         | Acceptable — MRP tidak real-time
                         | Log info bahwa stock_summary dipakai as-is
                         |
Quantity overflow        | Validate: qty > 0 dan < 1_000_000_000
                         | Jika invalid: skip demand, log warning
                         |
DB write failed          | Rollback seluruh transaksi
                         | Update mrp_runs = 'failed' (separate connection)
                         |
Job timeout (> 5 menit)  | GoLang context deadline exceeded
                         | BullMQ menganggap job failed, trigger retry
                         |
Unknown panic            | Recover(), log stack trace
                         | Update mrp_runs = 'failed'
                         | Job dikembalikan ke queue (BullMQ retry)
```

---

## 2.10 Configuration

```bash
# .env.example

# ─── Database ──────────────────────────────────────────
DATABASE_URL=postgres://erp_user:password@localhost:5432/erp_db
DB_MAX_CONNS=10                  # max connections di pool
DB_MIN_CONNS=2                   # min idle connections
DB_CONN_TIMEOUT_SECONDS=10       # timeout saat acquire connection

# ─── Redis ─────────────────────────────────────────────
REDIS_URL=redis://localhost:6379
REDIS_QUEUE_NAME=mrp-calculation  # KOREKSI: harus persis "mrp-calculation", bukan "mrp-jobs" — harus sama dengan nama queue di NestJS BullModule.registerQueue()
REDIS_CONSUMER_GROUP=mrp-workers

# ─── MRP Calculation ───────────────────────────────────
MRP_MAX_BOM_DEPTH=10             # proteksi circular BOM
MRP_DEFAULT_LEAD_TIME_DAYS=7     # fallback — CATATAN: kolom product_variants.lead_time_days sekarang NOT NULL DEFAULT 7 (migration 012), jadi fallback ini secara praktis tidak akan pernah terpakai kecuali ada gap data
MRP_DEFAULT_PRODUCTION_DAYS=3    # fallback jika tidak ada BOM operations
MRP_MIN_QUANTITY=0.001           # minimum quantity yang valid

# ─── Worker ────────────────────────────────────────────
MRP_WORKER_CONCURRENCY=3         # berapa job yang diproses paralel
MRP_JOB_TIMEOUT_MINUTES=10       # timeout per job
MRP_RETRY_ATTEMPTS=3             # KOREKSI: ini KHUSUS retry koneksi DB yang putus DI DALAM satu job run (internal Go). BUKAN retry job-level — itu sudah dimatikan di BullMQ (attempts:1) dan di-handle reconciliation NestJS terpisah, lihat FR-09

# ─── Logging ───────────────────────────────────────────
LOG_LEVEL=info                   # debug | info | warn | error
LOG_FORMAT=json                  # json | text (text untuk development)
APP_ENV=development              # development | production
```

---

## 2.11 Deployment

### Dockerfile

```dockerfile
# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o mrp-engine ./main.go

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app

COPY --from=builder /app/mrp-engine .
COPY .env.example .env

# Binary, bukan server — tidak expose port
CMD ["./mrp-engine"]
```

### PM2 Ecosystem (di VPS)

```javascript
// ecosystem.config.js (di VPS, bukan di repo)
module.exports = {
  apps: [
    {
      name: 'mrp-engine',
      script: '/app/mrp-engine/mrp-engine',
      interpreter: 'none',
      env: {
        APP_ENV: 'production',
        LOG_LEVEL: 'info',
      },
      // Auto-restart jika crash
      autorestart: true,
      max_restarts: 10,
      restart_delay: 5000,
    }
  ]
};
```

---

# 3. Task List

> **Konvensi:** Setiap task memiliki estimasi waktu dalam jam kerja (8 jam/hari).
> Total estimasi: **~72 jam kerja (~9 hari kerja)**

---

## Phase 1: Project Setup (Estimasi: 8 jam)

### Task 1.1 — Inisialisasi Repository
**Estimasi:** 1 jam

```
□ Buat repository baru: erp-mrp-engine
□ Initialize Go module:
    go mod init github.com/yourorg/erp-mrp-engine
□ Buat struktur direktori sesuai Section 2.4
□ Buat .gitignore (Go standard)
□ Buat .env.example dari Section 2.10
□ Buat README.md (placeholder)
□ Initial commit
```

### Task 1.2 — Go Dependencies
**Estimasi:** 1 jam

```
□ Install dependencies:
    go get github.com/jackc/pgx/v5              # PostgreSQL driver
    go get github.com/jackc/pgx/v5/pgxpool      # Connection pool
    go get github.com/redis/go-redis/v9          # Redis client
    go get github.com/joho/godotenv              # .env loading
    go get github.com/google/uuid                # UUID generation
□ Verify go.sum terbuat
□ Commit: "chore: add dependencies"
```

### Task 1.3 — Config Package
**Estimasi:** 2 jam

```
□ Buat config/config.go
□ Struct Config dengan semua field dari .env.example
□ Fungsi Load() yang:
    - Load .env file (jika ada)
    - Read dari os.Getenv
    - Validate required fields (DATABASE_URL, REDIS_URL)
    - Provide defaults untuk optional fields
□ Unit test: TestConfigLoad_AllRequired, TestConfigLoad_MissingRequired
□ Commit: "feat: config package"
```

### Task 1.4 — Logger Package
**Estimasi:** 2 jam

```
□ Buat internal/logger/logger.go
□ Wrapper di atas log/slog (Go 1.21+ standard library)
□ Support JSON dan text format (dari LOG_FORMAT env)
□ Support log levels: debug, info, warn, error
□ Fields standar: timestamp, level, service, tenant, run_id
□ Fungsi: Info(), Warn(), Error(), Debug() dengan context fields
□ Unit test: TestLogger_JsonFormat, TestLogger_TextFormat
□ Commit: "feat: logger package"
```

### Task 1.5 — Date Utilities
**Estimasi:** 2 jam

```
□ Buat internal/utils/date.go
□ Fungsi:
    - ParseDate(s string) (time.Time, error)
    - AddBusinessDays(t time.Time, days int) time.Time  -- v1: skip weekend tidak perlu
    - SubtractDays(t time.Time, days int) time.Time
    - IsBeforeToday(t time.Time) bool
□ Unit test untuk semua fungsi
□ Commit: "feat: date utilities"
```

---

## Phase 2: Database Layer (Estimasi: 12 jam)

### Task 2.1 — Connection Pool
**Estimasi:** 2 jam

```
□ Buat db/pool.go
□ Fungsi NewPool(cfg config.Config) (*pgxpool.Pool, error)
□ Konfigurasi:
    - MaxConns dari DB_MAX_CONNS
    - MinConns dari DB_MIN_CONNS
    - ConnectTimeout dari DB_CONN_TIMEOUT_SECONDS
    - HealthCheck interval: 30 detik
□ Fungsi Ping(ctx) error untuk health check
□ Test koneksi ke PostgreSQL lokal
□ Commit: "feat: database connection pool"
```

### Task 2.2 — Tenant Connection
**Estimasi:** 2 jam

```
□ Buat db/tenant.go
□ Struct TenantConn yang wraps *pgxpool.Conn
□ Fungsi AcquireTenant(ctx, pool, tenantCode) (*TenantConn, error):
    - Acquire connection dari pool
    - Execute: SET search_path TO "tenant_{code}", public
    - Return TenantConn
□ Method Release() untuk return ke pool
□ Validasi tenantCode: hanya huruf kecil, angka, underscore
□ Unit test: TestAcquireTenant_ValidCode, TestAcquireTenant_InvalidCode
□ Commit: "feat: tenant-aware connection"
```

### Task 2.3 — Models Package
**Estimasi:** 3 jam

```
□ Buat semua struct di models/ sesuai Section 2.5:
    □ models/demand.go       (MrpDemand, SubDemand, DemandGroup)
    □ models/stock.go        (StockEntry, OnOrderEntry)
    □ models/bom.go          (BomVersion, BomItem, MaterialRequirement)
    □ models/planned_order.go (PlannedOrder, PlannedOrderDemand)
    □ models/input_data.go   (InputData)
    □ models/calc_result.go  (CalcResult, CalcStats)
    □ models/variant_info.go (VariantInfo)
□ Semua field dengan tipe yang tepat (uuid sebagai string, qty sebagai float64)
□ JSON tags untuk logging
□ Commit: "feat: domain models"
```

### Task 2.4 — Query: Load Demands
**Estimasi:** 1 jam

```
□ Buat db/queries/demands.go
□ Fungsi LoadOpenDemands(ctx, conn, planFrom, planTo) ([]models.MrpDemand, error)
□ Implementasi Query 1 dari Section 2.7
□ Scan hasil ke []MrpDemand
□ Test dengan data fixture
□ Commit: "feat: query load demands"
```

### Task 2.5 — Query: Load Stock & On-Order
**Estimasi:** 1 jam

```
□ Buat db/queries/stock.go
□ Fungsi LoadStockSummary(ctx, conn, variantIDs) (map[string]map[string]float64, error)
□ Fungsi LoadOnOrder(ctx, conn, variantIDs) (map[string]map[string]models.OnOrderEntry, error)
□ Implementasi Query 2 dan 3 dari Section 2.7
□ Commit: "feat: query load stock and on-order"
```

### Task 2.6 — Query: Load BOM Data
**Estimasi:** 2 jam

```
□ Buat db/queries/bom.go
□ Fungsi LoadActiveBoms(ctx, conn) (map[string]string, map[string]float64, map[string]int, error)
    // Returns: activeBomsMap, outputQtyMap, leadTimeMap
□ Fungsi LoadAllBomItems(ctx, conn, bomVersionIDs) (map[string][]models.BomItem, error)
□ Implementasi Query 4 dan 5 dari Section 2.7
□ Commit: "feat: query load BOM data"
```

### Task 2.7 — Query: Writers
**Estimasi:** 1 jam

```
□ Buat db/queries/writer.go
□ Fungsi BulkInsertPlannedOrders(ctx, conn, orders []models.PlannedOrder) error
□ Fungsi BulkInsertPlannedOrderDemands(ctx, conn, links []models.PlannedOrderDemand) error
□ Fungsi BulkUpdateDemandStatus(ctx, conn, demandIDs []string, status string) error
□ Fungsi UpdateMrpRunCompleted(ctx, conn, runID string, stats models.CalcStats, durationMs int64) error
□ Fungsi UpdateMrpRunFailed(ctx, conn, runID, errMsg string) error
□ Implementasi Query 8-10 dari Section 2.7 menggunakan UNNEST untuk bulk operations
□ Commit: "feat: database writer queries"
```

---

## Phase 3: MRP Core Logic (Estimasi: 20 jam)

### Task 3.1 — Loader
**Estimasi:** 3 jam

```
□ Buat mrp/loader.go
□ Struct Loader dengan dependency ke db/queries
□ Fungsi LoadAll(ctx, conn, planFrom, planTo) (*models.InputData, error)
□ Orchestrate semua queries secara sequential (bukan parallel untuk v1)
□ Build semua maps yang dibutuhkan:
    - StockMap
    - OnOrderMap
    - ActiveBoms
    - BomItems
    - BomOutputQty
    - BomLeadTime
    - VariantInfo
    - SupplierHistory
□ Log jumlah data yang dimuat per kategori
□ Unit test dengan mock data
□ Commit: "feat: mrp loader"
```

### Task 3.2 — BOM Exploder
**Estimasi:** 5 jam

```
□ Buat mrp/bom_exploder.go
□ Struct BomExploder dengan InputData
□ Fungsi Explode(bomVersionID, parentQty, neededDate, warehouseID) ([]SubDemand, error)
□ Implementasi algoritma dari Section 2.8:
    □ Depth tracking (max depth check)
    □ Visited map untuk circular detection
    □ Phantom item handling
    □ Semi-finished goods recursive explosion
    □ Quantity calculation dengan scrap factor
□ Unit tests:
    □ TestExplode_SingleLevel
    □ TestExplode_TwoLevel
    □ TestExplode_ThreeLevel
    □ TestExplode_PhantomComponent
    □ TestExplode_CircularDetection → must return error
    □ TestExplode_MaxDepthExceeded → must return error
    □ TestExplode_EmptyBom → should return empty slice
    □ TestExplode_ScrapFactorCalculation
□ Commit: "feat: bom exploder"
```

### Task 3.3 — Scheduler
**Estimasi:** 2 jam

```
□ Buat mrp/scheduler.go
□ Struct Scheduler dengan config (defaultLeadTime, defaultProductionDays)
□ Fungsi CalculateProductionSchedule(neededDate, bomVersionID, input) Schedule
□ Fungsi CalculatePurchaseSchedule(neededDate, variantID, input) Schedule
□ Struct Schedule { PlannedStart, PlannedFinish, IsOverdue }
□ Logic:
    □ Produksi: lead_time dari BomLeadTime[bomVersionID] atau default
    □ Pembelian: lead_time dari VariantInfo[variantID].LeadTimeDays atau default
    □ PlannedStart = PlannedFinish - lead_time
    □ IsOverdue = PlannedStart < today
    □ Jika overdue: PlannedStart = today
□ Unit tests:
    □ TestSchedule_NormalCase
    □ TestSchedule_OverdueCase (needed_date di masa lalu)
    □ TestSchedule_DefaultLeadTime (variantInfo.LeadTimeDays = 0)
□ Commit: "feat: scheduler"
```

### Task 3.4 — Calculator
**Estimasi:** 6 jam

```
□ Buat mrp/calculator.go
□ Struct Calculator dengan BomExploder, Scheduler, InputData
□ Fungsi Calculate(input *models.InputData) (*models.CalcResult, error)
□ Implementasi logika utama:

    Step 1: Group demands per (variant_id, warehouse_id)
    □ Fungsi groupDemands(demands []MrpDemand) map[string]DemandGroup

    Step 2: Per group, hitung net requirement
    □ Fungsi calculateNetRequirement(group DemandGroup, input InputData) float64
    □ gross = sum(group.demands.quantity)
    □ onHand = input.StockMap[variantID][warehouseID]
    □ onOrder = input.OnOrderMap[variantID][warehouseID].Quantity
    □ net = gross - onHand - onOrder

    Step 3: Jika net <= 0 → fulfilled, lanjut ke group berikutnya

    Step 4: Jika net > 0 dan has BOM → planned production
    □ Fungsi createPlannedProduction(group, netQty, input) PlannedOrder
    □ Explode BOM → sub_demands
    □ Sub-demands ditambahkan ke processing queue (iterative, bukan recursive)

    Step 5: Jika net > 0 dan tidak ada BOM → planned purchase
    □ Fungsi createPlannedPurchase(group, netQty, input) PlannedOrder

    Step 6: Process sub-demands (dari BOM explosion)
    □ Ulangi Step 1-5 untuk sub-demands
    □ Aggregasi sub-demands yang sama variant+warehouse sebelum diproses

□ Assign demand allocations ke setiap planned order

□ Unit tests:
    □ TestCalculate_SufficientOnHand → no planned orders
    □ TestCalculate_SufficientOnOrder → no planned orders
    □ TestCalculate_PartialStock → planned order untuk sisanya saja
    □ TestCalculate_NoBom → planned purchase
    □ TestCalculate_WithBom → planned production + sub-demands
    □ TestCalculate_MultipleDemandsSameVariant → aggregated
    □ TestCalculate_MultiLevelBom → production + purchase for raw materials
    □ TestCalculate_MixedMtoMts → both types handled

□ Commit: "feat: mrp calculator"
```

### Task 3.5 — Writer
**Estimasi:** 2 jam

```
□ Buat mrp/writer.go
□ Struct Writer dengan dependency ke db/queries
□ Fungsi Write(ctx, conn, runID string, result *CalcResult) error:
    □ Begin transaction
    □ Bulk insert planned_orders
    □ Bulk insert planned_order_demands
    □ Bulk update mrp_demands status → 'planned'
    □ Commit
    □ Jika error: rollback, return error
□ Fungsi CompleteRun(ctx, conn, runID, result, durationMs) error
□ Fungsi FailRun(ctx, conn, runID, errMsg string) error
□ Unit test dengan mock DB (pgxmock)
□ Commit: "feat: mrp writer"
```

### Task 3.6 — Engine Orchestrator
**Estimasi:** 2 jam

```
□ Buat mrp/engine.go
□ Struct Engine dengan Loader, Calculator, Writer, Logger
□ Fungsi NewEngine(pool, cfg, logger) *Engine
□ Fungsi Run(ctx, job MrpJob) error:
    □ Log run start
    □ Acquire tenant connection
    □ Defer: release connection
    □ Call loader.LoadAll()
    □ Call calculator.Calculate()
    □ Call writer.Write()
    □ Call writer.CompleteRun()
    □ Log run complete dengan stats
    □ Error handling: call writer.FailRun() pada setiap error
□ Panic recovery dengan defer/recover
□ Commit: "feat: engine orchestrator"
```

---

## Phase 4: Queue Integration (Estimasi: 8 jam)

### Task 4.1 — Queue Models
**Estimasi:** 1 jam

```
□ Buat queue/models.go
□ Struct BullMQJob (sesuai Section 2.6)
□ Struct MrpJobData
□ Struct MrpJob (internal, sudah di-parse)
□ Fungsi ParseJob(raw string) (*MrpJob, error)
□ Unit test: TestParseJob_Valid, TestParseJob_Invalid
□ Commit: "feat: queue models"
```

### Task 4.2 — Redis Connection
**Estimasi:** 1 jam

```
□ Update config/config.go dengan Redis config
□ Buat fungsi NewRedisClient(cfg) (*redis.Client, error) di queue/consumer.go
□ Fungsi Ping() untuk health check
□ Commit: "feat: redis connection"
```

### Task 4.3 — Queue Consumer
**Estimasi:** 4 jam

```
□ Buat queue/consumer.go
□ Struct Consumer dengan redis, engine, logger, config
□ Fungsi NewConsumer(redis, engine, cfg, logger) *Consumer
□ Fungsi Start(ctx) — blocking, loop consume jobs
□ Implementasi BullMQ-compatible consumption:
    □ BLPOP dari "bull:{queue_name}:wait"
    □ Parse job payload
    □ Move ke "bull:{queue_name}:active"
    □ Call engine.Run(ctx, job)
    □ On success: move ke "bull:{queue_name}:completed"
    □ On failure: increment retry count, move ke wait atau failed
□ Goroutine pool untuk MRP_WORKER_CONCURRENCY parallel jobs
□ Context cancellation untuk graceful shutdown
□ Unit test dengan mock redis (miniredis)
□ Commit: "feat: queue consumer"
```

### Task 4.4 — Graceful Shutdown
**Estimasi:** 2 jam

```
□ Update main.go
□ Tangkap SIGTERM dan SIGINT
□ Cancel context → trigger shutdown di consumer
□ Wait untuk active jobs selesai (maksimal 60 detik)
□ Close DB pool dan Redis connection
□ Log "shutdown complete"
□ Test graceful shutdown: kirim SIGTERM saat job sedang berjalan
□ Commit: "feat: graceful shutdown"
```

---

## Phase 5: Main & Integration (Estimasi: 4 jam)

### Task 5.1 — Main Entry Point
**Estimasi:** 2 jam

```
□ Buat main.go
□ Flow:
    1. Load config
    2. Initialize logger
    3. Initialize DB pool (dengan retry jika DB belum ready)
    4. Initialize Redis client
    5. Initialize Engine
    6. Initialize Consumer
    7. Setup signal handling
    8. Start consumer (blocking)
    9. On shutdown: cleanup
□ Log startup message dengan versi dan config summary
□ Commit: "feat: main entry point"
```

### Task 5.2 — Health Check (opsional tapi direkomendasikan)
**Estimasi:** 2 jam

```
□ Tambah simple HTTP health endpoint (port 8080)
    GET /health → { "status": "ok", "db": "ok", "redis": "ok" }
□ Dipakai oleh PM2 atau monitoring untuk cek service hidup
□ Jangan expose ke public internet (hanya localhost atau internal network)
□ Commit: "feat: health check endpoint"
```

---

## Phase 6: Testing (Estimasi: 12 jam)

### Task 6.1 — Unit Test Coverage
**Estimasi:** 4 jam

```
□ Target coverage: minimum 70%
□ Pastikan semua test dari Task 3.2, 3.3, 3.4 sudah berjalan
□ Tambah test yang belum ada:
    □ config package
    □ date utilities
    □ loader (dengan mock queries)
    □ writer (dengan pgxmock)
□ Jalankan: go test ./...
□ Generate coverage report: go test -coverprofile=coverage.out ./...
□ Review: go tool cover -html=coverage.out
□ Commit: "test: unit test suite"
```

### Task 6.2 — Integration Test Setup
**Estimasi:** 2 jam

```
□ Buat test/integration/ directory
□ Setup test database:
    □ Script SQL untuk buat schema test
    □ Seed data: tenants, products, variants, warehouses, UoM
□ Buat helper fungsi:
    □ SetupTestDB() → koneksi ke test DB
    □ TeardownTestDB() → cleanup setelah test
    □ SeedDemands(n int) → buat n demands untuk test
□ Pastikan test bisa jalan dengan: TEST_DATABASE_URL=... go test -tags integration ./...
□ Commit: "test: integration test setup"
```

### Task 6.3 — Integration Tests: Core Scenarios
**Estimasi:** 4 jam

```
□ Scenario 1: Simple MTO (1 demand, BOM 1 level)
    Setup: SO demand 100 unit Kaos, stock 0, BOM: Kaos → Kain + Benang
    Expected: 1 planned_production (Kaos), 2 planned_purchase (Kain, Benang)

□ Scenario 2: Sufficient Stock
    Setup: demand 50 unit, stock on_hand 80 unit
    Expected: 0 planned orders, demand status = 'fulfilled' (handled di calculator)

□ Scenario 3: Partial Stock
    Setup: demand 100 unit, stock 40 unit
    Expected: planned order quantity = 60

□ Scenario 4: On-Order Coverage
    Setup: demand 100, stock 20, PO on-order 50
    Expected: planned order quantity = 30

□ Scenario 5: Multi-level BOM
    Setup: demand Kaos → BOM: Kaos → Kain Jadi → Kain Grey + Zat Pewarna
    Expected: planned_production Kaos, planned_production Kain Jadi,
              planned_purchase Kain Grey, planned_purchase Zat Pewarna

□ Scenario 6: Phantom Component
    Setup: BOM dengan Kain Jadi (is_phantom=true), tidak punya BOM sendiri
    Expected: Sub-komponen Kain Jadi di-skip, raw material-nya langsung dibuat

□ Scenario 7: Overdue Demand
    Setup: needed_date di masa lalu (2 minggu lalu)
    Expected: planned_start = today, is_overdue = true di log

□ Scenario 8: Multiple Demands Same Variant
    Setup: 3 demands untuk Kaos Merah XL dari SO berbeda
    Expected: 1 planned_production (aggregated), semua demand linked

□ Commit: "test: integration test scenarios"
```

### Task 6.4 — Performance Test
**Estimasi:** 2 jam

```
□ Setup data volume test:
    □ 20 produk dengan BOM 3 level masing-masing
    □ 200 demands (10 per produk)
    □ Stock data untuk 50% variant
□ Jalankan MRP run dan ukur:
    □ Total duration
    □ Memory usage (go tool pprof)
    □ Jumlah DB queries
□ Target: < 10 detik untuk 200 demands
□ Jika lebih lambat: identify bottleneck dan optimize
□ Commit: "test: performance baseline"
```

---

## Phase 7: Documentation & Deployment (Estimasi: 8 jam)

### Task 7.1 — README
**Estimasi:** 2 jam

```
□ Buat README.md komprehensif:
    □ Deskripsi singkat service
    □ Prerequisites (Go 1.22, PostgreSQL 16, Redis 7)
    □ Setup lokal (step by step)
    □ Environment variables (lengkap dari .env.example)
    □ Cara run: go run main.go
    □ Cara test: go test ./...
    □ Queue contract (format job JSON)
    □ Troubleshooting common issues
□ Commit: "docs: README"
```

### Task 7.2 — Dockerfile & Build
**Estimasi:** 2 jam

```
□ Buat Dockerfile (multi-stage) sesuai Section 2.11
□ Test build: docker build -t mrp-engine .
□ Test run: docker run --env-file .env mrp-engine
□ Pastikan binary ukurannya reasonable (< 20MB)
□ Buat .dockerignore
□ Commit: "chore: Dockerfile"
```

### Task 7.3 — Deployment ke VPS
**Estimasi:** 4 jam

```
□ Build binary untuk Linux amd64:
    GOOS=linux GOARCH=amd64 go build -o mrp-engine ./main.go

□ Upload ke VPS:
    scp mrp-engine user@vps:/app/mrp-engine/
    scp .env user@vps:/app/mrp-engine/

□ Setup PM2:
    pm2 start /app/mrp-engine/mrp-engine \
        --name mrp-engine \
        --interpreter none \
        --no-autorestart false

□ Verify berjalan:
    pm2 status
    pm2 logs mrp-engine

□ Test end-to-end:
    □ Trigger MRP run dari NestJS (POST /mrp/run)
    □ Monitor log GoLang: pm2 logs mrp-engine
    □ Cek planned orders terbuat di database
    □ Approve planned order dari NestJS
    □ Verify WO / PO draft terbuat

□ Setup auto-restart saat VPS reboot:
    pm2 startup
    pm2 save
```

---

## Phase 8: NestJS Side Updates — ✅ SELESAI (1 September 2026)

> **Status: sudah diimplementasi dan diverifikasi lewat spike test di `erp-backend`.** Task di bawah dipertahankan sebagai catatan retrospektif dan referensi struktur file, BUKAN task aktif yang perlu dikerjakan ulang. Beberapa detail berubah dari rencana awal — dicatat di setiap task.

### Task 8.1 — Install BullMQ di NestJS — ✅ Selesai

Sesuai rencana, tidak ada penyimpangan.

### Task 8.2 — MrpRunService — ✅ Selesai (dengan penyimpangan)

File aktual: `src/modules/production/services/mrp-run.service.ts`

Penyimpangan dari rencana:
- Queue diregister dengan nama **`mrp-calculation`**, bukan `mrp-jobs` seperti rencana awal (`BullModule.registerQueue({ name: 'mrp-calculation' })`)
- `triggerRun()` menambah pengecekan run aktif (`status IN ('pending','running')`) sebelum insert, plus try/catch eksplisit untuk `unique_violation` (23505) dari partial unique index — tidak ada di rencana awal karena requirement "satu run per tenant" belum punya mekanisme konkret saat draft awal ditulis
- `jobId` custom memakai format tenant-scoped `mrp-${tenantCode}-run-${runId}`
- `job.add()` pakai `attempts: 1`, bukan default/3x — retry job-level sepenuhnya dipindah ke reconciliation

### Task 8.3 — PlannedOrderService — ✅ Selesai

File aktual: `src/modules/production/services/planned-order.service.ts`. Sesuai rencana — `approve()`, `bulkApprove()` (nama beda dari `approveAll()` di rencana awal, fungsi sama), `cancel()`.

### Task 8.4 — Update ProductionController — ✅ Selesai (dengan penyimpangan path)

File aktual: `src/modules/production/production.controller.ts`. Path endpoint beda dari rencana awal:

| Rencana awal | Aktual |
|---|---|
| `POST /mrp/run` | `POST /mrp-runs` |
| `GET /mrp/runs` | `GET /mrp-runs` |
| `GET /mrp/runs/:id` | `GET /mrp-runs/:id` |
| `POST /mrp/planned-orders/:id/approve` | `POST /planned-orders/:id/approve` |
| `POST /mrp/runs/:runId/approve-all` | `POST /mrp-runs/:id/approve-all` |
| `POST /mrp/planned-orders/:id/cancel` | `POST /planned-orders/:id/cancel` |

Juga ditambah `POST /mrp-runs/:id/cancel` (tidak ada di rencana awal) untuk membatalkan run yang masih `pending`/`running`.

### Task 8.5 — Migration 012 — ✅ Selesai (jauh lebih besar dari rencana awal)

File aktual: `012_mrp_engine_service_support.sql` (tenant) + `004_create_mrp_service_role.sql` (public, dijalankan sebagai migration 007 di penomoran aktual — cek urutan file di repo).

Isi migration jauh lebih besar dari rencana awal ("index + lead_time_days column"), karena `mrp_runs` ternyata **sudah ada** sejak migration 007 (bukan tabel baru) — migration 012 jadi `ALTER TABLE`, bukan `CREATE TABLE`:
- Perluasan `status` CHECK constraint (`pending`/`running`/`completed`/`failed`/`failed_permanent`)
- Kolom baru: `retry_count`, `started_at`, `completed_at`, `job_schema_version`
- `product_variants.lead_time_days` — NOT NULL DEFAULT **7** (bukan 0 — sempat jadi bug diam-diam sebelum dikoreksi)
- Index reconciliation: `idx_mrp_runs_reconciliation (status, started_at) WHERE status='running'`
- Partial unique index concurrent-run: `idx_mrp_runs_one_active_per_tenant`
- GRANT permission role `mrp_service` (scoped: SELECT ke tabel referensi, SELECT+UPDATE `mrp_runs`, SELECT+INSERT `planned_orders`/`planned_order_demands`), dengan `RAISE WARNING` eksplisit kalau role belum ada saat migration jalan

Juga muncul isu operasional yang tidak ada di rencana awal: user database (`synkro_erp`) butuh privilege `CREATEROLE` eksplisit sebelum migration role bisa jalan (`ALTER USER synkro_erp WITH CREATEROLE;`).

---

## Checklist Final Sebelum Go-Live — Arsip Historis

Checklist dan tabel status di bagian akhir dokumen ini mencatat keadaan sebelum
implementasi selesai. Lihat `README.md` dan `docs/mrp-test-validation-report.md`
untuk status dan validasi terkini.

```
Pre-deployment:
  □ go test ./... → semua test pass
  □ Integration test semua 8 skenario → pass
  □ Performance test → < 10 detik untuk 200 demands
  □ Docker build berhasil
  □ .env production sudah dikonfigurasi
  □ DATABASE_URL dan REDIS_URL sudah benar

Deployment:
  □ NestJS migration 012 sudah dijalankan (lead_time_days column + indexes)
  □ GoLang binary sudah di-upload ke VPS
  □ PM2 service berjalan (pm2 status)
  □ Tidak ada error di log (pm2 logs mrp-engine --lines 50)

Smoke Test (Production):
  □ Login ke NestJS sebagai user dengan permission mrp:write
  □ POST /api/v1/mrp/run dengan plan horizon 3 bulan ke depan
  □ Monitor: pm2 logs mrp-engine (harus muncul log "run completed")
  □ GET /api/v1/mrp/runs/{runId} → status = 'completed'
  □ Lihat planned orders → ada hasil kalkulasi
  □ Approve satu planned order production → WO draft terbuat
  □ Approve satu planned order purchase → PO draft terbuat
  □ Verify tidak ada data dari tenant lain yang tercampur

Post-deployment:
  □ Monitor memory usage GoLang selama 24 jam pertama
  □ Cek tidak ada connection leak (DB pool size stabil)
  □ Verify log level sudah 'info' (bukan 'debug') di production
  □ Update project_analysis.md → centang MRP Engine
```

---

## Ringkasan Estimasi

| Phase | Deskripsi | Estimasi (jam) | Status |
|---|---|---|---|
| 1 | Project Setup | 8 | ⏳ Belum — stub minimal ada, belum full sesuai Task 1.1-1.5 |
| 2 | Database Layer | 12 | ⏳ Belum — stub `db.go`/`repository.go` ada, belum sesuai struktur Task 2.1-2.7 lengkap |
| 3 | MRP Core Logic | 20 | ⏳ Belum — BOM Exploder masih placeholder kosong |
| 4 | Queue Integration | 8 | ⚠️ Sebagian — stub worker pakai `gobullmq`, spike test happy path lolos, retry path belum divalidasi |
| 5 | Main & Integration | 4 | ⏳ Belum |
| 6 | Testing | 12 | ⏳ Belum |
| 7 | Documentation & Deployment | 8 | ⏳ Belum |
| 8 | NestJS Side Updates | ~~8~~ 0 | ✅ **Selesai** — lihat detail penyimpangan di Phase 8 |
| **Total sisa kerja** | | **~72 jam (~9 hari kerja)** | |

> **Catatan:** Estimasi Phase 1-7 belum disesuaikan turun meskipun sebagian pekerjaan stub sudah ada (project structure, model dasar, koneksi DB) — karena stub itu ditulis tanpa test coverage dan API `gobullmq` di dalamnya belum diverifikasi compile bersih. Anggap estimasi asli masih berlaku sampai stub benar-benar di-build dan diuji.

> **Catatan:** Estimasi mengasumsikan developer sudah familiar dengan GoLang.
> Jika ini adalah project GoLang pertama, tambahkan 30-50% waktu untuk learning curve.

---

*Dokumen ini mencakup PRD, System Design, dan Task List untuk MRP Engine v1.
Semua keputusan desain dalam dokumen ini final untuk v1 dan tidak berubah selama implementasi kecuali ada blocker teknis yang tidak terduga.*
