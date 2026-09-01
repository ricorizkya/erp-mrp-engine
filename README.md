# erp-mrp-engine — Stub Worker

Stub minimal untuk validasi end-to-end kontrak job (`mrp-job-contract.schema.json`)
antara `erp-backend` (NestJS/BullMQ) dan service ini. **Bukan implementasi MRP
calculation asli** — BOM exploder di `internal/domain/bom_exploder.go` masih
placeholder, lihat 8 test case wajib di spec sebelum Task 3.2 dianggap selesai.

## ⚠️ Wajib dilakukan sebelum percaya kode ini jalan benar

1. **Verifikasi API `gobullmq` sesuai versi yang ter-install.** Kode di
   `cmd/worker/main.go` ditulis berdasarkan signature yang terlihat di
   dokumentasi publik library per Agustus 2026 (`WorkerProcessAPI`, field
   `Job.Data`) — tapi ini library komunitas yang masih cepat berubah
   (release candidate). Cek `go doc go.codycody31.dev/gobullmq` setelah
   `go mod tidy` untuk konfirmasi signature persis sebelum asumsikan ini
   compile tanpa penyesuaian.

2. **Spike test kompatibilitas BullMQ version — ini paling kritis.**
   `gobullmq` menyatakan kompatibel dengan BullMQ v4.12.2. NestJS di
   `erp-backend` rencananya pin ke v5.x. Sebelum bangun logic MRP asli:
   - Push satu job dummy dari NestJS (`mrpQueue.add('mrp-calculation', {...})`)
   - Jalankan worker ini, pastikan job ke-pickup
   - Dari sisi NestJS, cek `await job.isActive()` — harus `true` selagi
     Go sedang proses
   - Kalau `false` atau error, berarti Go tidak berpartisipasi dengan benar
     di state machine BullMQ (lock token tidak ter-set lewat Lua script
     `moveToActive`), dan **reconciliation design di spec section 5 tidak
     valid** — perlu redesign (misal: heartbeat manual dari Go ke kolom
     `started_at`/kolom baru di `mrp_runs`, alih-alih mengandalkan
     `job.isActive()` BullMQ)

3. **Pin versi BullMQ NestJS eksplisit** ke versi v5.x tertentu di
   `package.json` (bukan `^5.0.0`), supaya tidak ke-resolve otomatis ke
   v6 yang baru rilis 30 Juli 2026 dengan pluggable backend berbeda total.

## Environment Variables

| Var | Contoh | Keterangan |
|---|---|---|
| `MRP_DATABASE_URL` | `postgres://mrp_service:xxx@pgbouncer-host:6432/erpdb` | Lewat PgBouncer, bukan direct ke Postgres |
| `REDIS_ADDR` | `redis-host:6379` | Sama Redis instance dengan BullMQ NestJS |

## Struktur

```
internal/domain/   # pure logic, tidak ada DB/Redis call, unit-testable independen
internal/infra/    # DB connection, repository, queue consumer
cmd/worker/        # entrypoint
```

## Yang belum diimplementasi (di luar scope stub ini)

- BOM exploder asli (`internal/domain/bom_exploder.go`) — masih return slice kosong
- Insert `planned_orders` + `planned_order_demands` — `TODO` di `repository.go`
- Net requirement calculation
- Role scoped Postgres (`mrp_service`) — GRANT sudah didefinisikan di
  `012_mrp_engine_service_support.sql`, tapi verifikasi USAGE ON SCHEMA
  dengan `CURRENT_SCHEMA()` masih perlu dites terhadap migration runner
  `erp-backend` yang sesungguhnya (lihat catatan di file migration)