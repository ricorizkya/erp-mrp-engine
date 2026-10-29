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
| Worker state machine | ✅ Berjalan | Claim pendek `pending → running`, guarded writer, retry/failure transitions |
| DB connection pool + tenant schema | ✅ Solid | `pgx` pool dengan `SET LOCAL search_path` per transaksi |
| Job contract parsing | ✅ Solid | `schemaVersion`, BIGINT IDs, RFC3339 dates |
| BOM exploder + net requirement | ✅ Berjalan | Unit-tested multi-level BOM, stock, on-order, phantom, dan over-demand |
| Backward scheduler | ✅ Berjalan | Lead time dan overdue demand diuji |
| DB load/write pipeline | ✅ Berjalan | Idempotent planned-order writer dan demand status update |
| Unit/race tests | ✅ Lulus | `go test ./...` dan `go test -race ./...` |
| Tagged integration tests | ✅ Lulus lokal | `TEST_DATABASE_URL` wajib menunjuk database test; schema dibuat terisolasi |
| Dockerfile + supervisor | ✅ Tersedia | Dockerfile dan systemd/supervisor artefak sudah ada |

> Lihat `docs/mrp-engine-full-plan.md` untuk task list lengkap dan estimasi waktu.

---

## Prerequisites

- **Go 1.22+** (project pakai Go 1.25, min requirement Go 1.22 untuk `range-over-func`)
- **PostgreSQL 16** dengan schema tenant yang sudah dijalankan migration 001-016
- **Redis 7+** — instance yang sama dengan NestJS backend
- PostgreSQL dapat diakses langsung atau lewat PgBouncer transaction mode
- Role Postgres `mrp_service` dibuat tanpa password oleh migration public
  `007_create_mrp_service_role.sql`, lalu password login dipasang dari secret lokal
  melalui `npm run db:provision-mrp-role` di backend

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
# MRP_DATABASE_URL=postgres://mrp_service:<password>@<postgres-or-pgbouncer-host>:5432/erpdb
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

# Integration tests (database/schema test dibuat ulang oleh helper)
TEST_DATABASE_URL="postgres://.../erp_test?sslmode=disable" go test -race -tags integration ./test/integration

# Race condition detection
go test -race ./...
```

### Deployment artifacts

- `Dockerfile` builds a static Linux worker image and runs it as UID 10001. Pass
  `MRP_DATABASE_URL` and `REDIS_ADDR` as runtime environment variables or secrets;
  they are not copied into the image.
- `scripts/worker_supervisor.sh` starts `bin/mrp-worker`; if the binary is absent,
  it builds it from the checked-out source before starting the restart loop.
- `deploy/systemd/mrp-worker.service` is a Linux systemd template. Adjust the
  installation directory and service account for the target host, and store
  runtime secrets in its `EnvironmentFile` outside Git.

---

## Environment Variables

| Variabel | Default | Keterangan |
|---|---|---|
| `MRP_DATABASE_URL` | — **(wajib)** | Koneksi PostgreSQL langsung atau lewat PgBouncer. Format: `postgres://mrp_service:<pwd>@<host>:5432/<db>` |
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
│   ├── config/                  ← env loading + validation
│   ├── logger/                  ← structured logging
│   ├── utils/                   ← date helpers
│   ├── domain/                  ← pure logic — tidak ada DB/Redis call di sini
│   │   ├── models.go            ← domain models (MrpJob, PlannedOrder, dll)
│   │   ├── bom_exploder.go      ← recursive multi-level BOM explosion
│   │   ├── calculator.go        ← net requirement calculation
│   │   └── scheduler.go         ← backward scheduling dari lead time
│   └── infra/
│       ├── db/
│       │   ├── db.go            ← pgx pool + WithTenantSchema helper
│       │   ├── repository.go    ← status updates (MarkRunning, MarkFailed, dll)
│       ├── mrp/                 ← engine, loader, guarded idempotent writer
│       └── queue/               ← BullMQ-compatible worker
├── deploy/systemd/mrp-worker.service
├── scripts/worker_supervisor.sh
├── docs/
│   ├── session.md               ← current status + historical decisions
│   ├── mrp-test-validation-report.md
│   └── mrp-engine-full-plan.md  ← archived design plan
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
  "id": "mrp-companyabc-run-3",
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
    "jobId": "mrp-companyabc-run-3",
    "attempts": 1,
    "removeOnComplete": false,
    "removeOnFail": false
  }
}
```

**Poin kritis:**

- `runId` dan `triggeredBy` adalah **integer (BIGINT)**, bukan UUID string
- `planFrom`/`planTo` harus format **RFC3339 lengkap** (`.toISOString()` dari NestJS)
- `jobId` wajib `mrp-${tenantCode}-run-${runId}` agar unik lintas schema tenant;
  run ID sendiri tidak global
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
- Pembatalan `pending/running → cancelled`; writer hanya menulis bila row masih `running`

---

## Keputusan Teknis Penting

Semua keputusan di bawah sudah di-commit ke schema dan code. Jangan ubah tanpa diskusi.

| Keputusan | Detail |
|---|---|
| Semua ID adalah `int64` | BIGINT GENERATED ALWAYS AS IDENTITY — Go tidak bisa pre-generate, harus `RETURNING id` setelah insert |
| `QueryExecModeSimpleProtocol` | **Wajib** untuk mendukung pgx + PgBouncer transaction mode tanpa prepared statement cache per koneksi |
| `SET LOCAL search_path` eksplisit | Setiap transaksi wajib set lokal `search_path TO tenant_xxx, public` agar aman pada pool/PgBouncer transaction mode |
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
- Pastikan `MRP_DATABASE_URL` mengarah ke PostgreSQL atau PgBouncer yang benar

**Job masuk tapi tidak diproses**
- Pastikan `REDIS_QUEUE_NAME` sama persis dengan nama queue di NestJS
- Redis queue name: `mrp-calculation` (bukan `mrp-jobs` atau variant lain)

**Error `permission denied for schema tenant_xxx`**
- Pastikan public migration 007 dan tenant migration 012-016 sudah dijalankan
- Jalankan `npm run db:provision-mrp-role` dari backend dengan
  `MRP_SERVICE_PASSWORD` tersedia hanya di environment lokal

**Error `Custom Id cannot be integers`**
- Pastikan NestJS menggunakan `jobId: mrp-${tenantCode}-run-${runId}`

**`started_at` tidak terupdate setelah job pickup**
- `MarkRunning` pakai `WHERE id=$1 AND status='pending'` — jika `rowsAffected=0`, berarti run sudah dipegang worker lain atau statusnya bukan `pending`

---

## Dokumentasi Lebih Lanjut

- [`docs/mrp-engine-full-plan.md`](./docs/mrp-engine-full-plan.md) — arsip PRD, System Design, dan task list; status saat ini mengikuti README ini
- [`docs/session.md`](./docs/session.md) — ringkasan keputusan arsitektur keseluruhan ERP (termasuk keputusan yang tidak boleh diubah)
- `mrp-job-contract.schema.json` — JSON Schema untuk validasi payload job
