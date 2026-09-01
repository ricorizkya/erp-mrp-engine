# erp-mrp-engine

[![Go Version](https://img.shields.io/badge/go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-proprietary-red)](./LICENSE)
[![Status](https://img.shields.io/badge/status-in%20development-yellow)](https://github.com/ricorizkya/erp-mrp-engine)

**MRP (Material Requirements Planning) Engine** — GoLang service untuk kalkulasi
kebutuhan material manufaktur secara asinkron. Bagian dari ERP Manufaktur SaaS
untuk industri tekstil, garmen, dan sejenis di Indonesia.

> Service ini **tidak expose HTTP endpoint publik**. Seluruh komunikasi melalui Redis
> queue (BullMQ format) yang di-push oleh NestJS backend (`erp-backend`).

---

## Arsitektur

```
NestJS Backend          Redis (BullMQ)       GoLang MRP Engine
     │                       │                      │
     │  POST /mrp-runs ──►   │  mrp-calculation ──► │
     │  (insert mrp_runs)    │  queue               │  1. Load demands, stock, BOM
     │                       │                      │  2. Net requirement calculation
     │                       │                      │  3. BOM explosion (multi-level)
     │                       │                      │  4. Generate planned orders
     │  GET /mrp-runs/:id ◄──┼──────────────────────┘
     │  (query planned_orders)                       │
     │                       │                  PostgreSQL 16
     │                       │             (schema per tenant)
```

Komunikasi data hanya lewat **PostgreSQL** (shared database, schema per tenant).
Go service membaca input dari `mrp_demands`, `stock_summary`, `bom_versions`, dan
menulis output ke `planned_orders` + `planned_order_demands`.

---

## Status Implementasi

| Komponen | Status | Keterangan |
|---|---|---|
| Spike test BullMQ ↔ Go | ✅ Lolos | `pending → running → completed` terverifikasi end-to-end |
| Stub worker (status pipeline) | ✅ Berjalan | Job masuk, status update, selesai |
| DB connection pool + tenant schema | ✅ Solid | `pgx` + `SimpleProtocol`, `WithTenantSchema` |
| Job contract parsing | ✅ Solid | `schemaVersion`, BIGINT IDs, RFC3339 dates |
| BOM Exploder logic | ⚠️ Stub | Placeholder, return kosong — belum diimplementasi |
| Net requirement calculator | ❌ Belum ada | |
| Backward scheduler | ❌ Belum ada | |
| DB queries (load + write) | ❌ Belum ada | Hanya status update, belum planned_orders insert |
| Unit tests | ❌ Belum ada | Target coverage ≥ 70% |
| Integration tests | ❌ Belum ada | 8 skenario dari spec |
| Dockerfile | ❌ Belum ada | |

> Lihat `docs/mrp-engine-full-plan.md` untuk task list lengkap dan estimasi waktu.

---

## Prerequisites

- **Go 1.22+** (project pakai Go 1.25, min requirement Go 1.22 untuk `range-over-func`)
- **PostgreSQL 16** dengan schema tenant yang sudah dijalankan migration 001-012
- **Redis 7+** — instance yang sama dengan NestJS backend
- **PgBouncer** — Go connect lewat PgBouncer (transaction mode), bukan direct ke Postgres
- Role Postgres `mrp_service` sudah dibuat via `004_create_mrp_service_role.sql`

---

## Setup Lokal

### 1. Clone dan install dependencies

```bash
git clone https://github.com/ricorizkya/erp-mrp-engine.git
cd erp-mrp-engine
go mod download
```

### 2. Konfigurasi environment

```bash
cp .env.example .env
# Edit .env — minimal wajib set:
# MRP_DATABASE_URL=postgres://mrp_service:<password>@<pgbouncer-host>:6432/erpdb
# REDIS_ADDR=<redis-host>:6379
```

Lihat bagian [Environment Variables](#environment-variables) untuk daftar lengkap.

### 3. Jalankan worker

```bash
go run ./cmd/worker
```

Output startup yang benar:
```
2026/09/01 10:00:46 mrp worker started (queue: mrp-calculation, concurrency: 5)
```

### 4. Jalankan tests

```bash
# Unit tests
go test ./...

# Dengan coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Integration tests (butuh DB lokal dengan migration sudah jalan)
TEST_DATABASE_URL="postgres://..." go test -tags integration ./...

# Race condition detection
go test -race ./...
```

---

## Environment Variables

| Variabel | Default | Keterangan |
|---|---|---|
| `MRP_DATABASE_URL` | — **(wajib)** | Koneksi ke PostgreSQL **lewat PgBouncer**, bukan direct. Format: `postgres://mrp_service:<pwd>@<host>:6432/<db>` |
| `REDIS_ADDR` | — **(wajib)** | Address Redis. Format: `<host>:6379` |
| `REDIS_QUEUE_NAME` | `mrp-calculation` | Harus identik dengan nama queue di NestJS `BullModule.registerQueue()` |
| `MRP_WORKER_CONCURRENCY` | `5` | Jumlah job yang diproses paralel |
| `MRP_JOB_TIMEOUT_MINUTES` | `10` | Timeout per job (deteksi stuck oleh reconciliation NestJS) |
| `MRP_MAX_BOM_DEPTH` | `10` | Proteksi circular BOM — error jika melampaui depth ini |
| `MRP_DEFAULT_LEAD_TIME_DAYS` | `7` | Fallback lead time untuk purchase (jika `product_variants.lead_time_days` NULL — praktis tidak terpakai karena kolom sekarang `NOT NULL DEFAULT 7` sejak migration 012) |
| `MRP_DEFAULT_PRODUCTION_DAYS` | `3` | Fallback lead time produksi jika BOM tidak punya operations |
| `MRP_MIN_QUANTITY` | `0.001` | Minimum quantity yang valid — di bawah ini diabaikan |
| `MRP_RETRY_ATTEMPTS` | `3` | **Khusus retry koneksi DB yang putus di dalam satu job run** (internal Go). Bukan job-level retry — itu di-handle `MrpReconciliationService` NestJS |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `LOG_FORMAT` | `json` | `json` untuk production, `text` untuk development lokal |
| `APP_ENV` | `development` | `development` / `production` |
| `DB_MAX_CONNS` | `10` | Max connections di pgx pool |
| `DB_MIN_CONNS` | `2` | Min idle connections |
| `DB_CONN_TIMEOUT_SECONDS` | `10` | Timeout acquire connection dari pool |

---

## Struktur Direktori

```
erp-mrp-engine/
├── cmd/
│   └── worker/
│       └── main.go              ← entrypoint: init config, DB, Redis, start worker
├── internal/
│   ├── config/                  ← [belum ada] env var loading + validation
│   ├── logger/                  ← [belum ada] structured JSON logging (log/slog wrapper)
│   ├── utils/                   ← [belum ada] date calculation helpers
│   ├── domain/                  ← pure logic — tidak ada DB/Redis call di sini
│   │   ├── models.go            ← domain models (MrpJob, PlannedOrder, dll)
│   │   ├── bom_exploder.go      ← [stub] recursive BOM explosion
│   │   ├── calculator.go        ← [belum ada] net requirement calculation
│   │   └── scheduler.go         ← [belum ada] backward scheduling dari lead time
│   └── infra/
│       ├── db/
│       │   ├── db.go            ← pgx pool + WithTenantSchema helper
│       │   ├── repository.go    ← status updates (MarkRunning, MarkFailed, dll)
│       │   └── queries/         ← [belum ada] semua SQL queries (load + write)
│       ├── mrp/                 ← [belum ada] loader + writer (orchestrate queries)
│       └── queue/               ← [belum ada] BullMQ consumer (gobullmq)
├── docs/
│   ├── session.md               ← session summary & keputusan arsitektur
│   └── mrp-engine-full-plan.md  ← PRD + System Design + Task List lengkap
├── go.mod
├── go.sum
└── .env.example
```

---

## Job Contract

Format JSON yang dikirim NestJS ke Redis queue dan dibaca service ini.
**Contract ini sudah divalidasi via spike test end-to-end (1 September 2026).**

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

**Poin kritis:**

- `runId` dan `triggeredBy` adalah **integer (BIGINT)**, bukan UUID string
- `planFrom`/`planTo` harus format **RFC3339 lengkap** (`.toISOString()` dari NestJS)
- `jobId` harus ber-prefix string `mrp-run-${runId}` — BullMQ v5 menolak jobId integer murni
- `schemaVersion` mismatch → Go reject dengan `failed_permanent` **langsung tanpa retry**
- `opts.attempts: 1` — job-level retry dimatikan di BullMQ, di-handle oleh `MrpReconciliationService` NestJS

---

## Status Transition

```
NestJS insert mrp_runs (status='pending')
  ↓
Push job ke BullMQ queue
  ↓
Go pickup job
  ↓
Go: UPDATE mrp_runs SET status='running' WHERE id=$1 AND status='pending'
  (jika rowsAffected=0: skip, job sudah dipegang worker lain)
  ↓
Kalkulasi MRP (load → calculate → write)
  ↓
Go: UPDATE mrp_runs SET status='completed' (dalam transaksi yang sama dengan insert planned_orders)
  ↓
Job selesai
```

**Error path:**
- Kegagalan transient → `status='failed'`, NestJS reconciliation retry (max 3x)
- Schema version mismatch → `status='failed_permanent'` langsung, skip retry
- Retry ke-3 gagal → NestJS set `status='failed_permanent'`

---

## Keputusan Teknis Penting

Semua keputusan di bawah sudah di-commit ke schema dan code. Jangan ubah tanpa diskusi.

| Keputusan | Detail |
|---|---|
| Semua ID adalah `int64` | BIGINT GENERATED ALWAYS AS IDENTITY — Go tidak bisa pre-generate, harus `RETURNING id` setelah insert |
| `QueryExecModeSimpleProtocol` | **Wajib** untuk pgx + PgBouncer transaction mode. Tanpa ini, prepared statement cache per-koneksi menyebabkan bug intermiten |
| `SET search_path` eksplisit | Setiap transaksi wajib set `search_path TO tenant_xxx, public` — tidak ada interceptor seperti NestJS |
| Write dalam satu transaksi | `planned_orders`, `planned_order_demands`, status update — commit/rollback bersama |
| Job retry: rekonsiliasi NestJS | `MRP_RETRY_ATTEMPTS` di Go = retry koneksi DB internal. Job-level retry = `MrpReconciliationService` NestJS |
| Queue name: `mrp-calculation` | Identik dengan `BullModule.registerQueue({ name: 'mrp-calculation' })` di NestJS |

---

## Koneksi ke Repo Lain

| Repo | Kaitan |
|---|---|
| `erp-backend` (NestJS) | Push job ke queue, consume hasil dari `planned_orders`, menjalankan reconciliation |
| `erp-frontend` (React + Vite) | Belum dimulai — akan menampilkan hasil MRP run dan planned orders |

**Jika job contract berubah** (field `data`, `opts`, `schemaVersion`): **kedua repo harus diupdate bersamaan**.

---

## Troubleshooting

**Worker startup gagal: `connection refused`**
- Pastikan PostgreSQL dan Redis berjalan dan bisa diakses dari mesin ini
- Cek `MRP_DATABASE_URL` mengarah ke PgBouncer, bukan direct ke PostgreSQL

**Job masuk tapi tidak diproses**
- Pastikan `REDIS_QUEUE_NAME` sama persis dengan nama queue di NestJS
- Redis queue name: `mrp-calculation` (bukan `mrp-jobs` atau variant lain)

**Error `permission denied for schema tenant_xxx`**
- Role `mrp_service` harus sudah dibuat via `004_create_mrp_service_role.sql`
- Migration 012 (`012_mrp_engine_service_support.sql`) harus sudah dijalankan di tenant schema
- User database butuh `CREATEROLE`: `ALTER USER synkro_erp WITH CREATEROLE;`

**Error `Custom Id cannot be integers`**
- Pastikan NestJS menggunakan format `jobId: mrp-run-${runId}` (dengan prefix string)

**`started_at` tidak terupdate setelah job pickup**
- `MarkRunning` pakai `WHERE id=$1 AND status='pending'` — jika `rowsAffected=0`, berarti run sudah dipegang worker lain atau statusnya bukan `pending`

---

## Dokumentasi Lebih Lanjut

- [`docs/mrp-engine-full-plan.md`](./docs/mrp-engine-full-plan.md) — PRD, System Design, Task List, semua SQL queries, algoritma BOM Exploder
- [`docs/session.md`](./docs/session.md) — ringkasan keputusan arsitektur keseluruhan ERP (termasuk keputusan yang tidak boleh diubah)
- `mrp-job-contract.schema.json` — JSON Schema untuk validasi payload job