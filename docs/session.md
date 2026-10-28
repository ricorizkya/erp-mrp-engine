# Status note — 2026-10-28

This document contains historical session notes. The current implementation
source of truth is the worker README and the backend `docs/planning.md`.

Verified baseline: unit/race tests pass; tagged PostgreSQL integration tests
cover visible claim, double-claim prevention, cancellation guard, idempotent
retry, and schema-version terminal failure. Worker claims runs in a short
transaction and writes only while status is `running`. E2E lokal dua tenant dari
NestJS melalui Redis/BullMQ ke worker Go mencapai `completed` pada 2026-10-28.

Semua bagian setelah catatan ini adalah histori keputusan. Klaim lama tentang
worker stub, job ID tanpa tenant, atau komponen yang belum dibuat tidak lagi
menggambarkan kode saat ini.

# Session Summary — ERP Manufaktur SaaS

> **Tanggal Session:** Agustus 2026 (update terakhir: sesi lanjutan MRP Engine GoLang)
> **Durasi:** Multi-session
> **Status Akhir:** Backend API selesai semua module. MRP Engine: kontrak job + migration 012 + stub Go worker selesai, implementasi BOM exploder asli dan spike test kompatibilitas BullMQ belum dimulai.

---

## Daftar Isi

1. [Konteks Awal & Scope](#1-konteks-awal--scope)
2. [Keputusan Tech Stack](#2-keputusan-tech-stack)
3. [Keputusan Arsitektur](#3-keputusan-arsitektur)
4. [Database Design Decisions](#4-database-design-decisions)
5. [Progress Backend — Module per Module](#5-progress-backend--module-per-module)
6. [File yang Sudah Dibuat](#6-file-yang-sudah-dibuat)
7. [Keputusan Penting yang Tidak Boleh Diubah](#7-keputusan-penting-yang-tidak-boleh-diubah)
7.5. [MRP Engine GoLang — Keputusan Detail (Sesi Lanjutan)](#75-mrp-engine-golang--keputusan-detail-sesi-lanjutan)
8. [Yang Belum Selesai & Next Steps](#8-yang-belum-selesai--next-steps)
9. [Catatan Teknis Penting](#9-catatan-teknis-penting)

---

## 1. Konteks Awal & Scope

### Produk yang Dibangun

SaaS ERP untuk industri manufaktur Indonesia. Target awal:
- **3 klien di tahun pertama**
- **Industri v1:** Tekstil dan Garmen (sebagai basis utama)
- **Industri v2:** Depot Air Minum (BPOM), Rokok (Cukai BKC)

### Modul yang Diputuskan untuk v1

Urutan phase pembangunan:

```
Phase 1: Inventory & Warehouse + Purchase Order
Phase 2: BOM + Production Planning (MRP) + Sales Order
Phase 3: Quality Control + Accounting & Finance
```

### Keputusan Scope Penting

- **Point of Sales (coffee shop, retail):** Ditolak untuk v1. ERP manufaktur dan POS adalah dua persona yang berbeda. Bisa ditambahkan v2 jika arsitektur modular.
- **Fixed Asset Management:** Defer ke v2
- **Budgeting:** Defer ke v2
- **Multi-currency:** Defer ke v2, v1 hanya IDR
- **SSO/OAuth:** Defer ke v2, v1 JWT murni
- **In-Process QC & Outgoing QC:** Defer ke v2, v1 hanya Incoming + Final QC
- **Lot Sizing, Capacity Planning:** Defer ke v2 di MRP Engine

---

## 2. Keputusan Tech Stack

### Backend

| Layer | Teknologi | Alasan |
|---|---|---|
| Core API | NestJS (TypeScript) | Modular architecture, TypeScript native |
| Computation Engine | GoLang | MRP calculation, report generation — CPU-bound |
| Frontend | React + Vite (TypeScript) | SPA murni, static build via Nginx |
| Database Utama | PostgreSQL 16 | Superior untuk relational data kompleks |
| Cache & Queue | Redis | Session, caching, BullMQ job queue |
| Object Storage | MinIO | Self-hosted S3-compatible |
| Query Builder | **Kysely** | Type-safe, hampir identik performa raw driver |
| UI Components | Shadcn/UI + Tailwind | |

### Keputusan yang Ditolak dan Alasannya

**Next.js ditolak** → React + Vite dipilih karena:
- ERP internal tidak butuh SSR atau SEO
- Next.js menjalankan Node.js server (~200-400MB RAM) — sia-sia untuk SPA
- React + Vite = static files, di-serve langsung dari Nginx, ~0MB overhead

**TypeORM/Prisma ditolak** → Kysely dipilih karena:
- TypeORM: terlalu banyak "magic", overhead 1-3ms per query
- Prisma: kurang cocok untuk multi-schema dinamis (tenant per schema)
- Kysely: type-safe, overhead < 0.3ms, SQL transparan, support raw SQL

**Raw pg driver murni ditolak** → Kysely dipilih karena:
- Type safety hilang di query layer
- Multi-tenant schema switching rawan human error (lupa SET search_path)
- Perbedaan performa raw vs Kysely tidak signifikan untuk volume ERP

**ORM lebih lambat dari raw query** → Ini benar tapi misleading:
- Overhead ORM: 1-3ms. Query execution PostgreSQL: 5-500ms
- Bottleneck ERP ada di: missing index, N+1 query, koneksi tidak di-pool
- Bukan di pilihan ORM

### Infrastruktur

- **Deployment:** Cloud provider lokal Indonesia (Biznet/IDCloudHost)
- **Arsitektur awal (3 klien):** 2 VPS saja

```
VPS 1 (App): Nginx + NestJS (PM2) + React static + GoLang MRP service
VPS 2 (DB):  PostgreSQL 16 + Redis + PgBouncer
CDN:         Cloudflare (free tier) — DDoS, SSL, CDN
Storage:     MinIO self-hosted di VPS
```

**Tidak langsung Kubernetes** karena: overhead manajemen cluster tidak sebanding untuk 3 klien. Scale saat sudah terbukti ada demand.

---

## 3. Keputusan Arsitektur

### Multi-Tenancy: Schema per Tenant ✅

**Dipilih:** PostgreSQL schema per tenant (`tenant_{code}`)

**Ditolak:**
- Shared schema + tenant_id: risiko data leak, trust issue untuk B2B
- Database per tenant: terlalu mahal di infrastruktur awal

**Implementasi:**
```sql
-- Setiap tenant dapat schema sendiri
CREATE SCHEMA tenant_companyabc;
SET search_path TO tenant_companyabc, public;
```

### Connection Pooling: PgBouncer + pg pool

**PgBouncer** di depan PostgreSQL untuk connection pooling terpusat:
- Mode: **transaction** (bukan session) — koneksi dikembalikan ke pool setelah transaksi, cocok untuk multi-tenant
- Catatan penting: dengan transaction mode, `SET search_path` tidak persistent → harus di-set di application layer setiap request

### Tenant Identification: JWT-based ✅

**Dipilih:** Tenant diidentifikasi via `tenantCode` di JWT payload

**Ditolak:** Subdomain per tenant (bisa ditambahkan v2 untuk enterprise)

Flow:
```
Login: email + password + kode_perusahaan
→ JWT payload: { userId, tenantId, tenantCode, roles, permissions }
→ Setiap request: TenantInterceptor baca tenantCode → SET search_path
```

### @TenantDb() Decorator Pattern

Problem: `getTenantDb()` mengembalikan `{ db, release }` dan caller wajib panggil `release()` — rawan connection leak.

**Solusi:** Kombinasi `TenantInterceptor` + `@TenantDb()` decorator:

```
Request masuk
→ TenantInterceptor: ambil koneksi, set search_path, simpan di req
→ @TenantDb() decorator: inject db dari req ke parameter method
→ Handler selesai
→ TenantInterceptor: finalize() → release koneksi (auto, tidak bisa lupa)
```

### Migration System: Custom Runner dengan Checksum

**Dipilih:** Custom migration runner (bukan TypeORM migrations, bukan Flyway)

Fitur:
- Track migration via `migration_history` table di public schema
- Checksum SHA256 per file — jika file diubah setelah dijalankan, sistem throw error
- `runTenantMigrations(tenantCode)` untuk tenant baru
- `runMigrationsForAllTenants()` untuk schema update

**Aturan tidak boleh dilanggar:** Jangan edit migration file yang sudah pernah dijalankan. Selalu buat file baru.

### Auto-numbering: Counter Table ✅

**Dipilih:** Counter table dengan `SELECT FOR UPDATE` (bukan PostgreSQL sequence per tahun)

```sql
CREATE TABLE document_counters (
    doc_type VARCHAR(20),
    year     INTEGER,
    counter  INTEGER DEFAULT 0,
    PRIMARY KEY (doc_type, year)
);
```

**Alasan:** Sequence PostgreSQL butuh DDL baru setiap tahun (CREATE SEQUENCE). Counter table reset otomatis karena `year` adalah bagian dari key.

**Kapasitas:** Counter table aman untuk ratusan dokumen per hari. Bottleneck hanya terjadi saat 50+ concurrent writers per detik — tidak akan terjadi di ERP manufaktur UKM.

Format nomor: `{DOC_TYPE}-{YYYY}-{NNNNN}` (contoh: `PO-2026-00001`)

DocType yang terdaftar: `PR`, `RFQ`, `PO`, `GR`, `VI`, `APV`, `SQ`, `SO`, `DO`, `INV`, `PAY`, `WO`, `QC`, `JE`

### MRP Engine: GoLang Terpisah, Komunikasi via Redis Queue

**Dipilih:** GoLang service terpisah, async via BullMQ Redis queue

**Alasan GoLang:**
- MRP adalah CPU-bound computation: recursive BOM explosion, net requirement calculation untuk ratusan demand
- Node.js single-threaded tidak cocok
- GoLang: goroutines, performa CPU-bound superior, compiled binary

**Alasan Queue (bukan HTTP sync):**
- MRP bisa memakan 10-60 detik untuk dataset besar
- HTTP request-response tidak cocok untuk long-running job
- Queue: user trigger tanpa menunggu, NestJS tetap responsive

**Repo terpisah:** `erp-mrp-engine` (bukan monorepo)

Alasan pisah repo:
- Runtime berbeda, lifecycle berbeda, cara deploy berbeda
- Go tidak pakai `node_modules` — tidak ada yang bisa di-share
- CI/CD lebih sederhana per repo
- Ownership lebih jelas

### Soft Reservation untuk Stock (Sales Order)

**Dipilih:** Soft reservation — tidak ada inventory movement untuk reserve

**Ditolak:** Hard reservation (dedicated RESERVED movement type)

**Alasan:**
- Hard reservation kompleks untuk sistem MTO: barang belum ada saat SO dibuat, mau reserve apa?
- Kalau SO di-confirm tapi delivery 3 bulan lagi, stock "terkunci" tidak bisa dipakai SO lain
- Solusi lebih elegan: `available_stock` materialized view

```sql
available_stock = quantity_on_hand - SUM(so_items.quantity_pending)
-- di mana SO status IN ('confirmed', 'partial')
```

Saat DO confirm: sistem cek `available_stock`, bukan `quantity_on_hand`. Kalau tidak cukup → BadRequestException dengan pesan jelas.

---

## 4. Database Design Decisions

### Stock Ledger (Append-Only) untuk Inventory

**Dipilih:** Tidak ada kolom `current_stock` yang di-update langsung.

Stock on hand dihitung dari `SUM of movements` via materialized view `stock_summary`.

**Keuntungan:**
- Tidak rawan race condition
- Sepenuhnya auditable
- `REFRESH MATERIALIZED VIEW CONCURRENTLY stock_summary` setelah setiap confirmed movement

### Product Variant sebagai Unit Terkecil

Semua tabel transaksional (inventory, PO, SO, BOM, Production) mereferensikan `product_variant_id`, bukan `product_id`.

**3 UoM per produk:**
- `base_uom_id` — satuan dasar
- `purchase_uom_id` — satuan saat beli
- `sales_uom_id` — satuan saat jual

Ini realita manufaktur: beli benang dalam kg, produksi dalam gram, jual dalam roll.

### 3 Boolean Flag (bukan type enum) untuk Products

```sql
can_be_purchased    BOOLEAN  -- bisa jadi raw material
can_be_sold         BOOLEAN  -- bisa dijual
can_be_manufactured BOOLEAN  -- bisa diproduksi
```

**Alasan:** Kain grey bisa jadi output di satu pabrik tapi input di pabrik lain dalam grup yang sama. Fixed enum tidak bisa handle ini.

### BOM: Multi-level + Versioning

- **Multi-level:** recursive via `parent_item_id` di `bom_items`
- **Versioning:** satu `bom_header` per produk, banyak `bom_versions`
- **Constraint DB:** partial unique index — hanya satu version berstatus `active` per header
- **Versi lama tetap tersimpan** untuk traceability produksi historis

### Accounting: Double-Entry Bookkeeping

- Setiap transaksi → minimal 2 journal entry lines
- `SUM(debit) = SUM(credit)` selalu, enforced saat posting
- Kolom `folio` untuk cross-reference antara jurnal dan buku besar
- Seed data: Chart of Accounts dasar 40+ akun sesuai SAK ETAP Indonesia

### DocumentNumberService: Dipindahkan ke CommonModule

**Awalnya:** `DocumentNumberService` ada di `PurchaseOrderModule`

**Diubah:** Dipindahkan ke `CommonModule` karena dipakai oleh semua module (SO, Production, QC, Accounting). Menaruh shared utility di domain-specific module adalah coupling yang salah.

---

## 5. Progress Backend — Module per Module

### ✅ Infrastructure & Foundation

| File | Status | Keterangan |
|---|---|---|
| `database.module.ts` | ✅ | @Global(), Pool provider |
| `database.service.ts` | ✅ | Kysely + tenant switching |
| `migration.service.ts` | ✅ | Runner dengan checksum SHA256 |
| `common.module.ts` | ✅ | TenantInterceptor + DocumentNumberService |
| `tenant-db.decorator.ts` | ✅ | @TenantDb() parameter decorator |
| `tenant.interceptor.ts` | ✅ | Lifecycle manager koneksi per request |
| `global-exception.filter.ts` | ✅ | Consistent error responses |
| `document-number.service.ts` | ✅ | Counter table + SELECT FOR UPDATE |
| `main.ts` | ✅ | Fastify, Helmet, CORS, Rate Limiting, Validation |
| `database.types.ts` | ✅ | Kysely types untuk semua tabel |

### ✅ Migrations (11 file)

| File | Isi |
|---|---|
| `000_create_migration_history.sql` | Tracking table |
| `001_create_tenants.sql` | Public schema: tenants |
| `002_create_users_roles.sql` | Public schema: users, roles, user_roles |
| `001_create_foundation.sql` | Tenant: branches, warehouses, uom |
| `002_create_product_master.sql` | Tenant: products, variants, attributes, batches, suppliers, customers |
| `003_create_inventory.sql` | Tenant: movements, stock_summary (MV) |
| `004_create_purchase_order.sql` | Tenant: PR, RFQ, PO, GR, vendor invoice |
| `005_create_sales_order.sql` | Tenant: quotation, SO, DO, invoice, payment |
| `006_create_bom.sql` | Tenant: BOM header, version, items, operations, by-products |
| `007_create_production_planning.sql` | Tenant: MRP demands, runs, planned orders, WO, materials |
| `008_create_quality_control.sql` | Tenant: QC parameters, checklists, inspections, defects |
| `009_create_accounting.sql` | Tenant: CoA, GL, journals, AP, AR, bank, tax, cost sheets + seed data |
| `010_create_document_counters.sql` | Tenant: counter table untuk semua DocType |
| `011_create_available_stock.sql` | Tenant: available_stock (MV) untuk soft reservation |

### ✅ AuthModule

- JWT Access Token (15 menit) + Refresh Token (7 hari)
- Refresh token: Database + Redis (Redis = cache, DB = fallback)
- RBAC: Module-Action matrix permissions
- `JwtAuthGuard` + `PermissionGuard` + `@RequirePermission()` decorator
- `@CurrentUser()` decorator
- Token blacklisting saat logout
- Refresh token rotation (satu token hanya bisa dipakai sekali)
- Rate limiting login: 5 percobaan per 15 menit per IP
- `TokenCleanupService`: cron job hapus expired tokens jam 02:00
- 6 role template default: admin, manager, staff_gudang, staff_produksi, staff_pembelian, akuntan

**Endpoints:**
```
POST /auth/login
POST /auth/refresh
POST /auth/logout
POST /auth/logout-all
POST /auth/change-password
GET  /auth/me
```

### ✅ ProductModule

Services: `ProductService`, `ProductCategoryService`, `AttributeService`, `UomService`, `BatchService`

**Endpoints:**
```
GET/POST        /products
GET/PATCH/DELETE /products/:id
GET/POST        /products/:id/variants
PATCH/DELETE    /products/variants/:variantId
GET/POST        /product-categories
PATCH/DELETE    /product-categories/:id
GET/POST        /attributes
POST            /attributes/:id/values
DELETE          /attributes/values/:valueId
GET/POST        /uom
GET/POST        /uom/conversions
GET             /products/variants/:variantId/batches
POST            /batches
```

### ✅ InventoryModule

Services: `WarehouseService`, `InventoryMovementService`, `StockQueryService`, `StockOpnameService`

Fitur penting:
- Stock ledger (append-only), tidak ada update langsung
- `REFRESH MATERIALIZED VIEW CONCURRENTLY stock_summary` setelah setiap confirmed movement
- Reorder alerts: variant dengan stock < min_stock
- Stock opname: snapshot → hitung fisik → auto-generate adjustment movements
- Adjustment movements auto-confirmed (tidak perlu approve manual)

**Endpoints:**
```
GET/POST        /branches, /warehouses, /warehouse-locations
GET/POST/PATCH  /warehouses/:id
GET/POST        /inventory-movements
POST            /inventory-movements/:id/confirm|cancel
GET             /stock, /stock/variants/:variantId, /stock/history
GET             /stock/locations/:warehouseId, /stock/reorder-alerts
GET/POST        /stock-opnames
POST            /stock-opnames/:id/complete|cancel
```

### ✅ PurchaseOrderModule

Services: `PurchaseRequestService`, `RfqService`, `PurchaseOrderService`, `GoodsReceiptService`, `VendorInvoiceService`

Alur: **PR → RFQ → PO → GR → Vendor Invoice**

Fitur penting:
- GR confirmed → otomatis buat inventory movement `PURCHASE_RECEIPT`
- GR confirmed → otomatis update `quantity_received` dan status PO
- Vendor Invoice bisa cover satu atau beberapa GR
- AP aging report
- Overdue invoice list

**Endpoints:**
```
GET/POST        /purchase-requests
POST            /purchase-requests/:id/submit|approve|reject
GET/POST        /rfqs
POST            /rfqs/:id/send
POST            /rfqs/:id/quotes (submit supplier quote)
POST            /rfqs/:id/quotes/:quoteId/select
GET/POST        /purchase-orders
PATCH           /purchase-orders/:id
POST            /purchase-orders/:id/confirm|cancel
GET/POST        /goods-receipts
POST            /goods-receipts/:id/confirm
GET/POST        /vendor-invoices
GET             /vendor-invoices/overdue
POST            /vendor-invoices/:id/cancel
```

### ✅ SalesOrderModule

Services: `SalesQuotationService`, `SalesOrderService`, `DeliveryOrderService`, `CustomerInvoiceService`, `PaymentReceiptService`

Alur: **Quotation → SO → DO → Customer Invoice → Payment Receipt**

Fitur penting:
- SO confirmed → `available_stock` MV di-refresh (soft reservation aktif)
- DO confirmed → inventory movement `SALES_DELIVERY` + update SO delivered qty
- DO confirmed → refresh `stock_summary` dan `available_stock`
- Payment receipt bisa dialokasikan ke beberapa invoice sekaligus
- Overdue invoice list

**Endpoints:**
```
GET/POST        /sales-quotations
PATCH           /sales-quotations/:id
POST            /sales-quotations/:id/send|accept|reject|cancel
GET/POST        /sales-orders
POST            /sales-orders/:id/confirm|cancel
GET/POST        /delivery-orders
POST            /delivery-orders/:id/confirm|cancel
GET/POST        /customer-invoices
GET             /customer-invoices/overdue
GET/POST        /payment-receipts
```

### ✅ BomModule

Service: `BomService`

Fitur penting:
- Multi-level BOM via recursive `parent_item_id`
- Versioning: satu version `active` per header (enforced by partial unique index)
- `activateVersion()`: set active, obsolete semua versi lain — dalam transaction
- `explodeBom()`: recursive CTE PostgreSQL untuk flatten semua level
- `is_phantom`: component phantom tidak dapat WO, langsung ke sub-komponennya
- `scrap_pct`: auto-hitung `quantity_with_scrap = quantity * (1 + scrap_pct/100)`
- `getActiveVersionForVariant()`: dipakai ProductionModule

**Endpoints:**
```
GET/POST        /bom
GET/PATCH       /bom/:id
GET             /bom-versions/:id
POST            /bom/:headerId/versions
PATCH           /bom-versions/:id
POST            /bom-versions/:id/activate
POST/PATCH/DELETE /bom-versions/:versionId/items
PATCH/DELETE    /bom-items/:id
POST/PATCH/DELETE /bom-versions/:versionId/operations
PATCH/DELETE    /bom-operations/:id
POST/DELETE     /bom-versions/:versionId/by-products
DELETE          /bom-by-products/:id
GET             /bom-versions/:id/explode?quantity=100
```

### ✅ ProductionModule

Services: `WorkOrderService`, `MrpDemandService`

Fitur penting:
- WO confirm → auto-generate WO materials dari BOM explosion
- WO confirm → auto-generate WO operations dari BOM operations
- Material consumption → inventory movement `PRODUCTION_OUT` per batch
- Production result → inventory movement `PRODUCTION_IN`
- Production result → auto-create batch baru jika `batchNumber` disediakan
- Status tracking: draft → confirmed → in_progress → completed/partially_done
- MTS vs MTO tracking (`production_type` field)

**Endpoints:**
```
GET/POST        /mrp-demands
POST            /mrp-demands/:id/cancel
GET/POST        /work-orders
PATCH           /work-orders/:id
POST            /work-orders/:id/confirm|start|cancel
POST            /work-orders/:id/consume-materials
POST            /work-orders/:id/production-results
POST            /work-order-operations/:id/start|complete
```

### ✅ QualityControlModule

Services: `QcMasterService`, `QcInspectionService`

Fitur:
- Incoming QC: link ke GR
- Final QC: link ke Production Result
- Database constraint: `CHECK` yang enforce incoming → GR, final → production_result
- Checklist-based inspection dengan parameter tipe: pass_fail, numeric, text
- Defect tracking dengan severity: critical, major, minor
- Disposition: accepted, accepted_with_debit, rework, rejected, pending
- Defect summary/analytics per periode
- Seed data: 10 QC parameters dan 11 defect types untuk tekstil/garmen

**Endpoints:**
```
GET/POST        /qc-parameters
DELETE          /qc-parameters/:id
GET/POST        /qc-checklists
GET             /qc-checklists/:id
DELETE          /qc-checklists/:id
GET/POST        /qc-defect-types
GET/POST        /qc-inspections
GET             /qc-inspections/defect-summary
GET             /qc-inspections/:id
POST            /qc-inspections/:id/complete|cancel
```

### ✅ AccountingModule

Services: `FiscalPeriodService`, `AccountService`, `JournalEntryService`, `GeneralLedgerService`, `ApService`, `ArService`, `BankService`

Fitur:
- Double-entry bookkeeping
- Validasi balanced: SUM(debit) = SUM(credit), toleransi 0.01
- Journal posting → update GL + account_balances
- Journal reversal → buat journal kebalikan, kedua linked
- `autoPost()` helper untuk modul lain (belum disambungkan — lihat section 8)
- Trial Balance, P&L (Income Statement), Balance Sheet, Cash Flow report
- AP aging dan AR aging
- Bank reconciliation
- Tax codes (PPN, PPh)
- Overhead rates untuk cost accounting
- Seed CoA: 40+ akun dasar SAK ETAP Indonesia
- System accounts (`system_account` field) untuk auto-posting mapping

**Endpoints:**
```
GET/POST        /fiscal-years
POST            /fiscal-years/:id/close
GET/POST        /accounting-periods
PATCH           /accounting-periods/:id/status
GET/POST        /accounts
PATCH           /accounts/:id
GET/POST        /journal-entries
POST            /journal-entries/:id/post|reverse
GET             /general-ledger, /account-balances
GET             /reports/trial-balance|profit-loss|balance-sheet|cash-flow
GET/POST        /ap-transactions, /ap-transactions/aging
GET/POST        /ap-payments
GET/POST        /ar-transactions, /ar-transactions/aging
GET/POST        /bank-accounts
GET             /bank-accounts/:id/transactions|reconciliations
POST            /bank-transactions, /bank-reconciliations
POST            /bank-reconciliations/:id/complete
GET/POST        /tax-codes, /overhead-rates
```

### ✅ Dokumentasi

| Dokumen | File | Keterangan |
|---|---|---|
| Technical Architecture Document | `ERP-Architecture-Document.docx` | PRD, stack, schema, flow, security plan |
| MRP Engine Plan | `mrp-engine-plan.md` | Architecture plan untuk GoLang service |
| MRP Engine Full Plan | `mrp-engine-full-plan.md` | PRD + System Design + Task List lengkap |

---

## 6. File yang Sudah Dibuat

### Struktur Akhir Project (erp-backend)

```
src/
├── app.module.ts
├── main.ts
├── types/
│   └── database.types.ts                  ← Kysely types semua tabel + TenantSchema
├── common/
│   ├── common.module.ts
│   ├── document-number.service.ts         ← Shared, dipindah dari PO module
│   ├── decorators/
│   │   └── tenant-db.decorator.ts
│   ├── filters/
│   │   └── global-exception.filter.ts
│   └── interceptors/
│       └── tenant.interceptor.ts
├── database/
│   ├── database.module.ts
│   ├── database.service.ts
│   ├── migration.service.ts
│   └── migrations/
│       ├── public/
│       │   ├── 000_create_migration_history.sql
│       │   ├── 001_create_tenants.sql
│       │   ├── 002_create_users_roles.sql
│       │   └── 003_create_refresh_tokens.sql
│       └── tenant/
│           ├── 001_create_foundation.sql
│           ├── 002_create_product_master.sql
│           ├── 003_create_inventory.sql
│           ├── 004_create_purchase_order.sql
│           ├── 005_create_sales_order.sql
│           ├── 006_create_bom.sql
│           ├── 007_create_production_planning.sql
│           ├── 008_create_quality_control.sql
│           ├── 009_create_accounting.sql
│           ├── 010_create_document_counters.sql
│           └── 011_create_available_stock.sql
└── modules/
    ├── auth/
    │   ├── auth.module.ts
    │   ├── auth.service.ts
    │   ├── auth.controller.ts
    │   ├── auth.constants.ts
    │   ├── auth.types.ts
    │   ├── token-cleanup.service.ts
    │   ├── dto/auth.dto.ts
    │   ├── guards/auth.guard.ts
    │   ├── decorators/current-user.decorator.ts
    │   └── strategies/jwt.strategy.ts
    ├── product/
    │   ├── product.module.ts
    │   ├── product.controller.ts
    │   ├── dto/product.dto.ts
    │   └── services/
    │       ├── product.service.ts
    │       ├── product-category.service.ts
    │       ├── attribute.service.ts
    │       ├── uom.service.ts
    │       └── batch.service.ts
    ├── inventory/
    │   ├── inventory.module.ts
    │   ├── inventory.controller.ts
    │   ├── dto/inventory.dto.ts
    │   └── services/
    │       ├── warehouse.service.ts
    │       ├── inventory-movement.service.ts
    │       ├── stock-query.service.ts
    │       └── stock-opname.service.ts
    ├── purchase-order/
    │   ├── purchase-order.module.ts
    │   ├── purchase-order.controller.ts
    │   ├── dto/purchase-order.dto.ts
    │   └── services/
    │       ├── purchase-request.service.ts
    │       ├── rfq.service.ts
    │       ├── purchase-order.service.ts
    │       ├── goods-receipt.service.ts
    │       └── vendor-invoice.service.ts
    ├── sales-order/
    │   ├── sales-order.module.ts
    │   ├── sales-order.controller.ts
    │   ├── dto/sales-order.dto.ts
    │   └── services/
    │       ├── sales-quotation.service.ts
    │       ├── sales-order.service.ts
    │       ├── delivery-order.service.ts
    │       └── customer-invoice.service.ts  ← include PaymentReceiptService
    ├── bom/
    │   ├── bom.module.ts
    │   ├── bom.controller.ts
    │   ├── dto/bom.dto.ts
    │   └── services/bom.service.ts
    ├── production/
    │   ├── production.module.ts
    │   ├── production.controller.ts
    │   ├── dto/production.dto.ts
    │   └── services/
    │       ├── work-order.service.ts
    │       └── mrp-demand.service.ts
    ├── quality-control/
    │   ├── quality-control.module.ts
    │   ├── quality-control.controller.ts
    │   ├── dto/qc.dto.ts
    │   └── services/
    │       ├── qc-master.service.ts
    │       └── qc-inspection.service.ts
    └── accounting/
        ├── accounting.module.ts
        ├── accounting.controller.ts
        ├── dto/accounting.dto.ts
        └── services/
            ├── fiscal-period.service.ts
            ├── account.service.ts
            ├── journal-entry.service.ts
            ├── general-ledger.service.ts
            ├── ap.service.ts
            ├── ar.service.ts
            └── bank.service.ts
```

**Total: ~90 file TypeScript + SQL**

---

## 7. Keputusan Penting yang Tidak Boleh Diubah

Ini adalah keputusan arsitektur yang sudah di-commit ke schema dan code. Mengubahnya butuh refactor besar.

### 7.1 Schema Multi-Tenant

```
JANGAN ubah: Struktur schema public (tenants, users, roles) dan tenant (semua tabel bisnis)
JANGAN hapus: Kolom system_account di tabel accounts — dipakai auto-posting
JANGAN ubah: Partial unique index idx_bom_versions_one_active
```

### 7.2 Foreign Key Hierarchy

Urutan dependency (yang lebih atas tidak boleh reference yang lebih bawah):
```
1. branches
2. warehouses
3. uom
4. product_categories, attributes
5. products
6. product_variants
7. batches
8. suppliers, customers
9. inventory_movements → inventory_movement_items
10. purchase_orders → goods_receipts → vendor_invoices
11. sales_orders → delivery_orders → customer_invoices
12. bom_headers → bom_versions → bom_items
13. mrp_demands → planned_orders → work_orders
14. qc_inspections (link ke GR atau production_results)
15. journal_entries → general_ledger
```

### 7.3 Stock Summary Refresh Order

**WAJIB** refresh dalam urutan ini setiap kali ada pergerakan inventory:
```
1. REFRESH MATERIALIZED VIEW CONCURRENTLY stock_summary
2. REFRESH MATERIALIZED VIEW CONCURRENTLY available_stock
```

Jika dibalik atau salah satu di-skip, available_stock akan stale.

### 7.4 DocumentNumberService di CommonModule

Jangan pindahkan kembali ke module manapun. Ini shared utility dan harus di `CommonModule` yang `@Global()`.

### 7.5 Queue Contract MRP

Format job antara NestJS dan GoLang:
```json
{
  "id": "bullmq-job-uuid",
  "name": "mrp-calculation",
  "data": {
    "runId": "uuid",
    "tenantSchema": "tenant_companyabc",
    "planFrom": "2026-01-01",
    "planTo": "2026-03-31",
    "triggeredBy": "user-uuid"
  }
}
```

Jika format ini berubah, **kedua repo harus diupdate bersamaan**.

---

## 7.5 MRP Engine GoLang — Keputusan Detail (Sesi Lanjutan)

> Dokumen lengkap: `mrp-engine-go-spec.md`. Section ini ringkasan untuk konteks cepat.

### Koreksi Penting Terhadap Asumsi Awal

- **`mrp_runs`, `planned_orders`, `planned_order_demands` SUDAH ADA** sejak `007_create_production_planning.sql` — bukan tabel baru yang perlu dibuat MRP Engine service.
- **PK `mrp_runs.id` dan `planned_orders.id` adalah `BIGINT GENERATED ALWAYS AS IDENTITY`, BUKAN UUID.** `runId` di job contract dan `triggeredBy` juga integer (BIGINT), bukan UUID string — draft awal sempat salah asumsi ini.
- Status existing `mrp_runs` cuma `'running' | 'completed' | 'failed'` — diperluas lewat migration 012 jadi `'pending' | 'running' | 'completed' | 'failed' | 'failed_permanent'`.

### Koneksi Database dari Go

| Aspek | Keputusan |
|---|---|
| Pooling | Lewat **PgBouncer** (sama seperti NestJS), bukan direct connection |
| Driver | `pgx`, dengan **`QueryExecModeSimpleProtocol` wajib di-set** (matikan prepared statement cache — PgBouncer transaction mode mengembalikan koneksi fisik antar transaksi, bisa bug intermiten kalau tidak dimatikan) |
| Role Postgres | Role terpisah dari NestJS: **`mrp_service`**, scoped — `SELECT` ke `product_variants`, `bom_headers/versions/items/operations`, `uom`, `warehouses`, `suppliers`; `SELECT+UPDATE` ke `mrp_demands`, `mrp_runs`; `SELECT+INSERT` ke `planned_orders`, `planned_order_demands` |
| Multi-tenant | Go wajib `SET search_path` manual di tiap transaksi (tidak ada interceptor pattern seperti NestJS) |

### Status Transition & Idempotency

```
pending → running → completed
                   → failed → (retry, runId SAMA) → running
                            → failed_permanent (retry_count >= 3, atau schemaVersion mismatch langsung)
```

- Idempotency key = `runId` (BIGINT dari `mrp_runs.id`), row diinsert NestJS **sebelum** push job ke queue
- **Go yang set `pending → running`** (bukan NestJS) via direct SQL — bukti nyata job di-pickup. Pakai `WHERE status='pending'` di clause update, bukan cuma `WHERE id=?`, untuk defense terhadap race condition kalau ada >1 worker instance
- Update status final + insert `planned_orders`/`planned_order_demands` **dalam satu SQL transaction** di Go — commit/rollback bersama
- `schemaVersion` mismatch di job contract → **reject + `failed_permanent` langsung, skip retry** (bukan transient error)

### Reconciliation (di NestJS, `@nestjs/schedule`)

- Cron tiap **1 menit**, cari `status='running'` AND `started_at < NOW() - 5 menit`
- **Wajib cek status job di BullMQ dulu** (`job.isActive()`) sebelum retry — kalau job masih aktif di queue, jangan retry (proses mungkin masih hidup wajar, cuma lambat)
- Retry pakai `runId` sama, increment `retry_count`, reset `started_at`
- Cap retry: **3x**, backoff sederhana (retry tidak boleh berturut dalam satu cron cycle yang sama)
- Setelah 3x gagal → `status='failed_permanent'`
- BullMQ job config: **`attempts: 1`** (retry built-in BullMQ dimatikan) — reconciliation NestJS jadi satu-satunya mekanisme retry, supaya tidak ada dua layer retry yang tumpang tindih

### Notifikasi `failed_permanent`

**Keputusan v1:** tidak ada notification system aktif. Cukup visible lewat query `mrp_runs` di halaman MRP run list. Notification module (push/toast/badge) didefer — belum ada di scope manapun di `erp-backend`.

### ⚠️ Risiko Belum Tervalidasi: Kompatibilitas BullMQ ↔ Go

Catatan lama ("GoLang pakai raw Redis BLPOP, kompatibel BullMQ v5") **tidak akurat** — BullMQ pakai Lua script atomic (`moveToActive`) untuk job locking, bukan BLPOP sederhana. Tidak ada Go client resmi dari BullMQ. Library komunitas yang dipakai di stub (`go.codycody31.dev/gobullmq`) eksplisit menyasar **BullMQ v4.12.2**, sementara NestJS rencana pakai v5.x — dan **BullMQ v6 baru rilis 30 Juli 2026** dengan perubahan besar (pluggable queue backend, `IQueueBackend` abstraction).

**Wajib dilakukan sebelum lanjut implementasi MRP asli:** spike test — push job dummy dari NestJS, jalankan Go stub worker, verifikasi `job.isActive()` di sisi Node benar-benar `true` selagi Go memproses. Kalau gagal, desain reconciliation di atas perlu direvisi (kemungkinan pakai heartbeat manual dari Go ke `mrp_runs`, bukan mengandalkan BullMQ punya sendiri). Pin versi `bullmq` NestJS eksplisit ke v5.x tertentu, hindari `^5.0.0` yang bisa ke-resolve ke v6.

### Struktur Folder Go

```
erp-mrp-engine/
├── internal/
│   ├── domain/     # pure logic, no DB/Redis — Models (Task 2.3), BOM Exploder (Task 3.2)
│   └── infra/       # db (pgx pool + search_path helper), queue (BullMQ consumer), repository
├── cmd/worker/main.go
└── go.mod
```

### Test Case Wajib — BOM Exploder (sebelum Task 3.2 dianggap selesai)

1. Single-level BOM, qty bulat
2. Multi-level BOM (3+ level nested)
3. Qty pecahan (rounding error tidak boleh terakumulasi)
4. **Circular BOM reference** — harus return error, bukan infinite loop
5. BOM versioning — pakai versi aktif sesuai `effective_date`
6. Nested manufactured item (komponen yang punya BOM sendiri)
7. BOM kosong / produk tanpa BOM — return error jelas, bukan panic
8. Net requirement dikurangi stock on hand, bukan gross requirement mentah

### File yang Sudah Dibuat (Sesi Ini)

| File | Isi |
|---|---|
| `mrp-engine-go-spec.md` | Spec lengkap — semua keputusan di atas dengan detail dan rasional |
| `004_create_mrp_service_role.sql` (public) | `CREATE ROLE mrp_service` — password placeholder, wajib diganti sebelum deploy |
| `012_mrp_engine_service_support.sql` (tenant) | `ALTER TABLE mrp_runs` (status + kolom baru), `lead_time_days`, index reconciliation, GRANT permission `mrp_service` |
| `mrp-job-contract.schema.json` | JSON Schema kontrak job — `schemaVersion`, `runId` (integer), `tenantSchema`, `planFrom/To`, `triggeredBy` (integer) |
| `erp-mrp-engine-stub/` | Stub Go worker: `internal/domain` (models + exploder placeholder), `internal/infra/db` (pgx pool + repository), `cmd/worker/main.go`. BOM exploder logic **belum diimplementasi** — cuma pipeline status transition yang jalan |

### Catatan Belum Terverifikasi

- `GRANT USAGE ON SCHEMA` di migration 012 pakai `CURRENT_SCHEMA()` — perlu dites terhadap `migration.service.ts` yang sesungguhnya, apakah `search_path` benar ter-set ke tenant schema (bukan `public`) saat migration file dieksekusi
- Nama constraint `mrp_runs_status_check` di migration 012 adalah asumsi nama default Postgres — cek dengan `\d mrp_runs` sebelum run
- API `gobullmq` di stub ditulis berdasarkan dokumentasi publik, belum di-compile-test — cek `go doc` setelah `go mod tidy`

---

## 8. Yang Belum Selesai & Next Steps

### 8.1 Auto-Posting Accounting (PRIORITAS TINGGI)

**Status:** `JournalEntryService.autoPost()` sudah ada, tapi **belum terhubung** ke modul lain.

Yang perlu disambungkan:

| Event | Journal Entry yang Harus Dibuat |
|---|---|
| GR confirmed | Debit: Persediaan Bahan Baku / Credit: Hutang Usaha |
| DO confirmed | Debit: HPP / Credit: Persediaan Barang Jadi |
| WO completed | Debit: Persediaan Barang Jadi / Credit: Biaya Produksi |
| Customer Invoice dibuat | Debit: Piutang Usaha / Credit: Penjualan + PPN |
| Customer Payment | Debit: Bank / Credit: Piutang Usaha |
| Vendor Payment | Debit: Hutang Usaha / Credit: Bank |

**Cara sambungkan:** Inject `AccountingModule` ke `PurchaseOrderModule`, `SalesOrderModule`, `ProductionModule`. Hati-hati circular dependency — gunakan `forwardRef()` jika perlu.

**Langkah:**
1. Tambah `AccountingModule` ke imports di module yang butuh
2. Inject `JournalEntryService` ke service yang trigger event
3. Panggil `autoPost()` di dalam transaction yang sama dengan event-nya
4. Test bahwa journal terbuat dan balanced

### 8.2 MRP Engine GoLang (repo `erp-mrp-engine-stub`, belum jadi repo Git terpisah)

**Status:** Kontrak job, migration 012, dan stub worker (status transition pipeline) selesai. **BOM exploder asli belum diimplementasi** — masih placeholder return kosong. Lihat section 7.5 untuk detail lengkap.

**Dokumen:** `mrp-engine-full-plan.md` (58KB, task breakdown asli), `mrp-engine-go-spec.md` (spec keputusan sesi lanjutan)

**Blocker sebelum lanjut ke implementasi asli:** Spike test kompatibilitas BullMQ ↔ Go (lihat section 7.5) — belum dilakukan. Ini prioritas berikutnya, bukan langsung lanjut BOM exploder.

**Urutan berikutnya:**
1. Bangun endpoint trigger minimal di NestJS (lihat 8.3) untuk push job dummy
2. Jalankan stub worker, verifikasi `job.isActive()` dari sisi Node — spike test kompatibilitas BullMQ
3. Kalau spike test lolos: lanjut implementasi Task 2.3/3.2 asli (BOM Exploder) dengan 8 test case wajib di section 7.5
4. Kalau spike test gagal: redesign reconciliation (kemungkinan heartbeat manual, bukan `job.isActive()`)

### 8.3 NestJS Side untuk MRP Engine

**Status:** Belum dibuat sama sekali — ini yang paling mendesak sekarang, dibutuhkan untuk spike test di 8.2.

**Yang perlu dibuat (versi minimal dulu, untuk spike test):**
- Install `@nestjs/bullmq`, definisikan queue `mrp-calculation` dengan `attempts: 1`
- Satu endpoint trigger minimal: insert row `mrp_runs` (status default `pending` dari migration 012), ambil `id` hasil insert (`RETURNING id`), push job sesuai `mrp-job-contract.schema.json`

**Yang perlu dibuat setelah spike test lolos (scope penuh):**
- `MrpRunService` (trigger + monitor MRP runs, lengkap)
- `PlannedOrderService` (approve → buat WO/PO draft, bulk approve)
- Update `ProductionController` (tambah endpoints baru)
- Reconciliation cron job (`@nestjs/schedule`) sesuai desain di section 7.5

### 8.4 Migration 012 — SELESAI

File: `012_mrp_engine_service_support.sql`. **Bukan `CREATE TABLE`** seperti draft awal — `mrp_runs`/`planned_orders` sudah ada dari migration 007, jadi isinya `ALTER TABLE` (perluasan status, kolom `retry_count`/`started_at`/`completed_at`/`job_schema_version`), index reconciliation, `lead_time_days` di `product_variants`, dan GRANT permission untuk role `mrp_service` (dibuat di `004_create_mrp_service_role.sql`, public schema).

**Belum diverifikasi:** nama constraint `mrp_runs_status_check` (asumsi default Postgres) dan perilaku `CURRENT_SCHEMA()` di GRANT USAGE ON SCHEMA terhadap migration runner asli — cek sebelum run di environment manapun selain local dev.

### 8.5 is_within_spec untuk Numeric QC

**Status:** Di `qc-inspection.service.ts`, `is_within_spec` untuk parameter numeric di-set `true` secara default (simplified).

**Yang perlu diperbaiki:** Query `qc_parameters` untuk compare `numericValue` dengan `min_value` dan `max_value` lalu set `is_within_spec` yang benar.

### 8.6 Frontend (React + Vite)

**Status:** Belum dimulai, belum ada repo.

Akan dibuat di repo terpisah `erp-frontend`.

### 8.7 Cost Accounting (HPP)

**Status:** Schema sudah ada (`production_cost_sheets`, `production_cost_details`), tapi kalkulasi HPP belum diimplementasi.

Rencana: GoLang cost engine (bisa jadi bagian dari `erp-mrp-engine` atau service terpisah).

---

## 9. Catatan Teknis Penting

### Dependencies yang Perlu Di-install di NestJS

```bash
# Core
npm install kysely pg @nestjs/config

# Auth
npm install @nestjs/passport @nestjs/jwt passport passport-jwt
npm install bcrypt class-validator class-transformer
npm install @types/bcrypt @types/passport-jwt -D

# Queue (untuk MRP)
npm install @nestjs/bullmq bullmq

# Redis
npm install @nestjs-modules/ioredis ioredis

# Fastify plugins
npm install @fastify/helmet @fastify/cors @fastify/rate-limit

# Schedule (untuk token cleanup)
npm install @nestjs/schedule

# Types
npm install @types/pg -D
```

### Route Ordering Fastify

Fastify lebih strict dari Express soal route ordering. Static routes harus sebelum dynamic:

```typescript
// BENAR: /vendor-invoices/overdue sebelum /vendor-invoices/:id
@Get('vendor-invoices/overdue')  // ini dulu
@Get('vendor-invoices/:id')       // ini sesudah
```

Sudah benar di semua controller karena method `findOverdue` selalu dideklarasikan sebelum `findOne`.

### Naming Conflict Index Migration 005

File `005_create_sales_order.sql` punya index `idx_pr_*` untuk `payment_receipts` yang konflik dengan `idx_pr_*` di `004_create_purchase_order.sql` untuk `purchase_requests`.

**Status:** Perlu diperbaiki sebelum migration dijalankan pertama kali. Rename:
```sql
-- Ubah di 005:
idx_pr_customer     → idx_payment_receipt_customer
idx_pr_payment_date → idx_payment_receipt_date
```

Migration belum pernah dijalankan (masih di development), jadi bisa langsung edit file.

### Transfer Antar Gudang di Inventory

Transfer menggunakan **dua baris** di `inventory_movement_items`:
- Baris 1: `from_warehouse_id` diisi, `to_warehouse_id` NULL
- Baris 2: `from_warehouse_id` NULL, `to_warehouse_id` diisi

Logic ini di service layer, bukan di database. Sudah di-handle di `InventoryMovementService`.

### Vendor Invoice — Kalkulasi Subtotal Simplified

Di `VendorInvoiceService.create()`, saat ini:
```typescript
const subtotal  = Math.round(total / 1.11 * 100) / 100; // asumsi PPN 11%
const taxAmount = total - subtotal;
```

Ini simplified. Untuk production, subtotal dan tax harus dihitung dari GR items yang di-link, bukan flat 11% dari total PO. Perlu perbaikan saat integrasi dengan AccountingModule.

### AP Payment — Bank Account Fallback

Di `ApService.createPayment()`, jika `bankAccountId` tidak disediakan:
```typescript
const cashAccount = await this.accountService.getSystemAccount(db, 'cash');
```

Pastikan akun dengan `system_account = 'cash'` sudah ada di CoA. Seed data migration 009 hanya membuat akun dengan kode `1-1-001` (Kas) tapi tidak set `system_account`. Perlu ditambahkan:
```sql
UPDATE accounts SET system_account = 'cash' WHERE code = '1-1-001';
```

### GoLang MRP: BullMQ Compatibility

GoLang menggunakan raw Redis operations (`BLPOP`) yang kompatibel dengan BullMQ v5. Pastikan NestJS menggunakan BullMQ v5+ (bukan v3 atau v4 yang format queue key-nya berbeda).

---

### Bug Fix: BullMQ jobId Deduplication — Silent Retry No-Op (2026-08-31)

**Bug kritis di `MrpReconciliationService`**: Tiga keputusan yang masing-masing masuk akal sendiri-sendiri, tapi saling bertabrakan saat digabung:

1. `jobId: runId.toString()` — deterministik agar reconciliation bisa `getJob(runId)` ✅
2. `removeOnComplete: false, removeOnFail: false` — job lama tetap ada di Redis setelah selesai/gagal ✅
3. Reconciliation retry: `mrpQueue.add(..., { jobId: runId.toString() })` — push job baru dengan jobId SAMA ❌

BullMQ men-dedupe berdasarkan `jobId`. Karena job lama tidak pernah dihapus (poin 2), saat reconciliation coba `add()` job baru dengan `jobId` yang sama (poin 3), BullMQ mengembalikan referensi ke job lama yang sudah `failed` — bukan membuat entry baru di status `waiting`. Worker tidak pernah memproses ulang. Reconciliation mengira sudah retry (increment `retry_count`, log sukses), padahal job tidak pernah dieksekusi — sampai 3x habis dan `failed_permanent`.

**Fix**: Simpan `existingJob` di outer scope (bukan di dalam inner `try`), lalu panggil `existingJob.remove()` sebelum `mrpQueue.add()` saat retry. Audit trail tetap aman karena `retry_count` & `error_message` dicatat di `mrp_runs` (Postgres).

```typescript
// SEBELUM (broken — silent no-op)
await this.mrpQueue.add('mrp-calculation', payload, { jobId: runId.toString() });

// SESUDAH (benar)
if (existingJob) await existingJob.remove(); // hapus dulu dari Redis
await this.mrpQueue.add('mrp-calculation', payload, { jobId: runId.toString() });
```

### Bug Fix: Race Condition di `triggerRun()` — 500 Error Bocor (2026-08-31)

**Gap**: `MrpRunService.triggerRun()` pakai pola SELECT-then-INSERT yang secara inheren rentan race condition. Dua request bersamaan bisa sama-sama lolos `SELECT` sebelum salah satu sempat `INSERT`. Pertahanan terakhir ada di database (`idx_mrp_runs_one_active_per_tenant`), tapi kalau `unique_violation` (PG `23505`) tidak ditangkap eksplisit, dia bocor sebagai raw Postgres error 500.

**Fix**: Wrap INSERT dalam `try/catch`, tangkap `err.code === '23505'` dan lempar `ConflictException` dengan pesan yang jelas. Konstanta `PG_UNIQUE_VIOLATION = '23505'` ditambahkan di top-level file.

### Migration 012 — Patch Terbaru (2026-08-31)

Tiga perubahan tambahan dari review code:

| Perubahan | Detail |
|-----------|--------|
| `lead_time_days DEFAULT 0 → 7` | Default 0 adalah bug diam-diam — semua produk yang belum di-set lead time akan dihitung MRP dengan `planned_start = planned_finish` (tidak realistis). Default 7 konsisten dengan asumsi FR-07 di `mrp-engine-full-plan.md` |
| `idx_mrp_runs_one_active_per_tenant` | Partial unique index baru: `CREATE UNIQUE INDEX ... ON mrp_runs ((1)) WHERE status IN ('pending', 'running')`. Pertahanan DB-level terhadap concurrent run. Bekerja bersama try/catch `23505` di aplikasi |
| `RAISE WARNING` di GRANT block | Sebelumnya GRANT block silent no-op kalau role `mrp_service` belum ada. Sekarang mengeluarkan `RAISE WARNING` eksplisit dengan instruksi remediation |

### Catatan: Stray `.ts` Files di Root Workspace

File `.ts` yang ditempel langsung di root workspace (`/backend-erp/*.ts`) akan dikompilasi TypeScript (dengan `moduleResolution: nodenext`) dan menyebabkan error `TS2307: Cannot find module`. Pastikan **tidak ada file `.ts` di luar direktori `src/`**. Gunakan `ls *.ts` di root untuk verifikasi.

### Bug Fix: UnknownDependenciesException `MrpReconciliationService` (2026-09-01)

- **Masalah**: Constructor `MrpReconciliationService` meng-inject `private readonly pool: Pool`, yang diinterpretasikan NestJS sebagai dependensi `BoundPool`. Token `Pool` tidak diekspor oleh `DatabaseModule`.
- **Solusi**: Dependensi `Pool` dihapus dari constructor. Pengambilan daftar tenant aktif diganti menggunakan query builder Kysely `this.databaseService.getPublicDb().selectFrom('tenants').select('code').where('is_active', '=', true).execute()`, konsisten dengan service lain.

### Database Fix: Permission Denied `007_create_mrp_service_role.sql` (2026-09-01)

- **Masalah**: Saat aplikasi boot, `MigrationService` gagal menjalankan `007_create_mrp_service_role.sql` dengan error `permission denied to create role` karena user `synkro_erp` belum memiliki hak `CREATEROLE`.
- **Solusi**: Diberikan hak akses melalui `ALTER USER synkro_erp WITH CREATEROLE;`. Migrasi public 007 dan migrasi tenant 012 berhasil dijalankan sepenuhnya (role `mrp_service` terbuat dan permission `GRANT` aktif).

### Bug Fix: BullMQ Custom ID "Custom Id cannot be integers" (2026-09-01)

- **Masalah**: BullMQ v5 melarang custom `jobId` berupa angka murni (seperti `"1"` atau `"2"`). Mengirim `jobId: runId.toString()` menyebabkan exception saat `mrpQueue.add()`.
- **Solusi**: Format `jobId` diubah menjadi ber-prefix string `mrp-run-${runId}` di `MrpRunService` dan `MrpReconciliationService`.

### Bug Fix: Job Contract Serialization RFC3339 & Integer `triggeredBy` (2026-09-01)

- **Masala---

### Hasil Validasi Runtime & Chaos Test MRP Engine (2026-10-11)

Pengujian komprehensif pipeline penuh dan skenario kegagalan runtime (Load Test 200/500/1000 demands dan Test B-1a s.d B-4b) menghasilkan temuan dan perbaikan penting:

1. **Perbaikan Idempotensi Writer Go (`DeletePlannedOrdersByRunID`)**:
   - *Temuan*: Pada retry run yang sama, worker menduplikasi planned orders ($N \rightarrow 2N$).
   - *Solusi*: Ditambahkan pembersihan atomik `DELETE FROM planned_order_demands` dan `DELETE FROM planned_orders WHERE mrp_run_id = $1` sebelum bulk insert di `writer.go` & `repository.go`. Terverifikasi 100% idempotent saat re-run.
2. **Pemisahan Migration 013 (`013_mrp_service_grants.sql`)**:
   - *Temuan*: Penambahan GRANT manual pada `012_mrp_engine_service_support.sql` yang sudah ter-apply berisiko checksum mismatch error di environment/tenant lain.
   - *Solusi*: File 012 di-revert, tambahan `GRANT UPDATE ON mrp_demands` dan `GRANT DELETE ON planned_orders, planned_order_demands` dipindahkan ke migrasi baru `013_mrp_service_grants.sql`. Runner berhasil apply tanpa error.
3. **Keamanan PgBouncer Transaction Pooling (`SET LOCAL search_path`)**:
   - *Temuan*: `SET search_path` biasa dapat menempel pada koneksi server PgBouncer dan berisiko cross-tenant leakage.
   - *Solusi*: Diubah menjadi `SET LOCAL search_path TO %s, public` di `internal/infra/db/db.go`.
4. **Validasi Isolasi Handler PG 23505 (B-4b)**:
   - Dilakukan uji isolasi dengan menonaktifkan pre-check aplikasi sementara: dua request paralel membuktikan respon `HTTP 409` memuat pesan handler 23505 (`MRP Run lain baru saja mulai berjalan...`) tanpa pernah membocorkan error 500. Pre-check kemudian diaktifkan kembali.
5. **Validasi Assertion Kuantitas (Skala 200, 500, 1000)**:
   - Kuantitas planned orders tervalidasi 100% sesuai nilai harapan di sheet `Kuantitas` Excel (Kaos: 6.900/17.250/34.500 pcs, Kain: 1.725/4.312,5/8.625 m, Kapas: 2.070/5.175/10.350 kg). Seluruh selisih = 0.00 (Status: OK).
6. **Chaos Worker Crash (B-3) & Supervisi**:
   - Terbukti H1 (job tertahan di `active`) dan H2 (reconciliation sengaja skip selama worker mati).
   - Sistem pulih otomatis setelah worker hidup kembali.
   - **Prasyarat Deploy Terbuka**: Worker wajib disupervisi (systemd/PM2/container restart policy) dan ditambahkan alerting untuk run `running` > 10 menit atau `failed_permanent`.
7. **Pengujian Multi-Tenant Nyata (Item Terbuka)**:
   - Pengujian simultan dengan 2 schema tenant berbeda dijadwalkan sebelum gate Engine Selesai dinyatakan tercapai.

---

## Ringkasan Status Akhir

```
✅ Tech Stack & Architecture — Keputusan final, terdokumentasi
✅ Database Schema — 15 migration files (termasuk 012 & 013_mrp_service_grants.sql)
✅ AuthModule — JWT, RBAC, refresh token, security
✅ ProductModule — products, variants, attributes, UoM, batches
✅ InventoryModule — warehouses, movements, stock, opname
✅ PurchaseOrderModule — PR → RFQ → PO → GR → Invoice
✅ SalesOrderModule — Quotation → SO → DO → Invoice → Payment
✅ BomModule — multi-level BOM + versioning
✅ ProductionModule — MRP demands + Work Orders + MRP Runs + Planned Orders
✅ QualityControlModule — incoming + final QC
✅ AccountingModule — CoA, GL, journals, AP, AR, bank, reports
✅ Dokumentasi — Architecture doc, MRP Engine plan, Baseline & Validation Report
✅ Migration 013 — Tambahan GRANT permissions role mrp_service (UPDATE mrp_demands, DELETE planned_orders)
✅ MRP Engine GoLang — Idempotency Fix (Clean-before-write per runID)
✅ MRP Engine GoLang — SET LOCAL search_path untuk keamanan PgBouncer
✅ Validasi Runtime B-1a s.d B-4b — Dedup, requeue, handler 23505, quantity assertions verified
⏳ Supervisi Worker (systemd/PM2) & Alerting (run > 10m / failed_permanent) — Prasyarat deploy
⏳ Multi-Tenant Test (2 tenant simultan) — Prasyarat gate engine selesai
⏳ Frontend React + Vite (repo baru, belum dimulai)
```

---
*Session summary ini dibuat berdasarkan seluruh percakapan dalam satu session.*
*File ini harus diupdate setiap kali ada session baru yang membuat keputusan penting.*
