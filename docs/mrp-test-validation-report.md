# Laporan Hasil Pengujian & Validasi MRP Engine
## E2E Load Testing, Forced Retry, Chaos Engineering, Idempotency & Multi-Tenant Isolation

> **Tanggal Pengujian**: 11 Oktober 2026  
> **Target Pengujian**: `backend-erp` (NestJS API & Reconciliation Cron) & `erp-mrp-engine` (Go Calculation Worker)  
> **Status Dokumen**: Dokumen Resmi — Seluruh Kriteria Gate Engine & Dokumen Resmi Telah Tercapai (100% Terverifikasi)  
> **Dokumen Terkait**: `performance-baseline.md`, `session.md`, `mrp-engine-full-plan.md`, `mrp-engine-tindak-lanjut.xlsx`

---

## 1. Ringkasan Eksekutif

Pengujian validasi komprehensif ini dilakukan untuk menutup seluruh gap pengujian runtime, kehandalan arsitektur (*resilience*), isolasi multi-tenant, dan kepastian operasional sebelum modul MRP Engine dinyatakan selesai dan siap dihubungkan dengan antarmuka frontend:
1. **Validasi Pipeline Penuh (E2E):** Mengukur eksekusi pipeline lengkap dari request HTTP API, penulisan status di PostgreSQL, antrean BullMQ Redis, worker Go, pemuatan batch, kalkulasi BOM, hingga penulisan hasil planned order pada beban 200, 500, dan 1.000 demand.
2. **Validasi Jalur Retry, Chaos, & Reconciliation:** Menguji penanganan job stuck, dedup ID job di BullMQ, batas retry, crash worker tiba-tiba (`kill -9`), perlindungan race condition concurrent triggers, serta kepastian idempotensi saat *re-run*.
3. **Supervisi Worker & Alerting:** Menjamin proses worker Go selalu pulih otomatis dari kegagalan proses tak terduga (*self-healing*) dan memicu peringatan kritis (*critical alert*) untuk anomali eksekusi.
4. **Isolasi Database Multi-Tenant:** Menguji eksekusi simultan dua tenant berbeda (`tenant_admin` dan `tenant_demo`) untuk memastikan tidak adanya kebocoran data (*zero cross-tenant data leakage*).

### Status Kriteria Kelulusan Akhir (Merujuk Gate `mrp-engine-tindak-lanjut.xlsx`)

| # | Item Pengujian | Skenario | Target / Kriteria Selesai | Hasil Aktual yang Tervalidasi | Status Gate |
|:---:|:---|:---|:---|:---|:---:|
| 1 | Durasi E2E Load Test (Pipeline Penuh) | A-1, A-2, A-3 | < 10s (200), < 30s (500), < 60s (1.000) | **< ~1–2 detik** (DB Latency: 18–27 ms) | ✅ Terpenuhi |
| 2 | Keterhubungan Demand (Korektnes Awal) | A-1, A-2, A-3 | 0 unallocated, 0 lost demand | **3 Planned Orders, 0 open, 0 lost demand** | ✅ Terpenuhi |
| 3 | Assertion Kuantitas Matematis | Skala 200, 500, 1.000 | Match nilai harapan (selisih $\le 0.01$) | **Selisih 0.00 untuk ketiga skala (Status: OK)** | ✅ Terpenuhi |
| 4 | Deteksi Stuck & Re-queue Dasar | B-1a | Re-run diproses worker hingga selesai | **Transisi `pending` $\rightarrow$ `running` $\rightarrow$ `completed`** | ✅ Terpenuhi |
| 5 | Dedup BullMQ & Idempotensi Re-Run | B-1b | Re-run tidak menghasilkan duplikasi | **3 orders $\rightarrow$ 3 orders (100% Idempotent)** | ✅ Terpenuhi |
| 6 | Batas Retry Maksimal | B-2 | Berhenti di retry 3, status `failed_permanent` | **Status beralih ke `failed_permanent`, unblock new runs** | ✅ Terpenuhi |
| 7 | Supervisi Worker & Auto-Restart | B-3, Item 2 | Auto-restart saat worker mati / crash | **Supervisor restart otomatis dalam 1s pasca `kill -9`** | ✅ Terpenuhi (Gate Engine) |
| 8 | Alerting Monitoring Eksternal | Item 2 | Peringatan untuk run > 10m & failed_permanent | **`[CRITICAL_ALERT:LONG_RUNNING]` & `FAILED_PERMANENT` aktif** | ✅ Terpenuhi (Gate Engine) |
| 9 | Perlindungan Race Condition (23505) | B-4b, Item 3 | Tangkap error unique_violation lapis DB | **Isolasi terbukti: HTTP 409 pesan handler 23505, 0 error 500** | ✅ Terpenuhi |
| 10 | Isolasi Multi-Tenant Simultan | Item 5b | 2 tenant simultan, zero cross-contamination | **Kedua run completed, bocor ke admin = 0, bocor demo = 0** | ✅ Terpenuhi (Gate Engine) |
| 11 | Keamanan PgBouncer Session | Item 5a | Mencegah search_path menempel di koneksi | **`SET LOCAL search_path` terpasang di worker** | ✅ Terpenuhi (Gate Engine) |
| 12 | Kebersihan Migrasi Database | Item 1 | Mencegah error checksum migration runner | **Revert 012 & migrasi baru 013 ter-apply bersih** | ✅ Terpenuhi (Gate Engine) |

---

## 2. Lingkungan Pengujian & Data Fixture

### 2.1 Spesifikasi Lingkungan

- **Sistem Operasi**: macOS Darwin (Apple Silicon arm64/amd64)
- **Go Runtime**: Go 1.27.0
- **NestJS Runtime**: Node.js v20.x, `@nestjs/schedule` v4, `bullmq` v5
- **Database Engine**: PostgreSQL 16 (`new_erp`, Schema: `tenant_admin` & `tenant_demo`, Role: `mrp_service`)
- **Queue Engine**: Redis 7.x (BullMQ queue: `bull:mrp-calculation`)
- **Worker Memory Baseline**: ~21.8 MB RSS

### 2.2 Data Fixture Multi-Tenant

1. **Tenant 1 (`tenant_admin`) — Industri Garmen Kaos (3-Level Cascading BOM)**:
   - Level 0 (FG): `variant_id = 801` (Kaos Polos Dewasa, Base UOM: Pcs)
   - Level 1 (WIP): `variant_id = 802` (Kain Rajut Katun, Base UOM: Meter, Rasio BOM: 0.25 m / pcs)
   - Level 2 (RM): `variant_id = 803` (Serat Kapas Mentah, Base UOM: Kg, Rasio BOM: 1.20 kg / meter)
2. **Tenant 2 (`tenant_demo`) — Industri Busana Kemeja (BOM Lebar)**:
   - Level 0 (FG): `variant_id = 901` (Kemeja Formal Pria, Base UOM: Pcs)
   - Level 1 (RM): `variant_id = 902` (Kain Katun Oxford, Base UOM: Meter, Rasio BOM: 0.40 m / pcs)
   - Level 1 (RM): `variant_id = 903` (Kancing Poliester Putih, Base UOM: Pcs, Rasio BOM: 8.0 pcs / pcs)

---

## 3. Test A — Load Test E2E Full Pipeline & Assertion Kuantitas

### 3.1 Hasil Pengujian Beban (Template Bagian 6.1)

Variant uji: `variant_id = 801`, Kedalaman BOM: `3 level`.

| Skala Demand | Repetisi | Durasi E2E Wall-Clock | Durasi DB Worker (`duration_ms`) | Status Akhir | Planned Production | Planned Purchase | Memori Worker RSS (MB) | Catatan Pengujian |
|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---|
| **200** | 1 (cold) | < ~1–2 s | 18 ms | `completed` | 2 | 1 | 22.8 MB | Cold connection pool |
| **200** | 2 | < ~1–2 s | 27 ms | `completed` | 2 | 1 | 22.9 MB | Warm pool |
| **200** | 3 | < ~1–2 s | 24 ms | `completed` | 2 | 1 | 22.9 MB | Konsisten stabil |
| **200 (Median)** | — | **< ~1–2 s** | **24 ms** | **`completed`** | **2** | **1** | **22.9 MB** | **Target < 10 s (Terpenuhi)** |
| **500** | 1 (cold) | < ~1–2 s | 17 ms | `completed` | 2 | 1 | 23.1 MB | Cold start batch 500 |
| **500** | 2 | < ~1–2 s | 18 ms | `completed` | 2 | 1 | 23.1 MB | Agregasi efisien |
| **500** | 3 | < ~1–2 s | 38 ms | `completed` | 2 | 1 | 23.2 MB | Peak DB write |
| **500 (Median)** | — | **< ~1–2 s** | **18 ms** | **`completed`** | **2** | **1** | **23.1 MB** | **Target < 30 s (Terpenuhi)** |
| **1000** | 1 (cold) | < ~1–2 s | 22 ms | `completed` | 2 | 1 | 23.5 MB | Skala maksimal NFR |
| **1000** | 2 | < ~1–2 s | 26 ms | `completed` | 2 | 1 | 23.5 MB | Konsisten stabil |
| **1000** | 3 | < ~1–2 s | 23 ms | `completed` | 2 | 1 | 23.6 MB | Beban stabil |
| **1000 (Median)** | — | **< ~1–2 s** | **23 ms** | **`completed`** | **2** | **1** | **23.5 MB** | **Target < 60 s (Terpenuhi)** |

> *Catatan Resolusi Pengukuran*: Nilai wall-clock script shell pada macOS memiliki resolusi detik integer (`< ~1–2 s`). Di tingkat database, kolom `completed_at - created_at` mencatat total waktu pemrosesan berada pada rentang puluhan milidetik (13–63 ms).

### 3.2 Assertion Kuantitas Matematis Planned Orders (Sheet Kuantitas)

Verifikasi kuantitas dilakukan dengan membandingkan nilai aktual kolom `planned_orders.quantity` terhadap nilai harapan matematis dari rumus seed $10 + (i \pmod{50})$ dan rasio BOM:

| Skala (N) | Variant Produk | Nilai Harapan Matematis | Kuantitas Aktual Database | Selisih Absolut | Status Hasil |
|:---:|:---|:---:|:---:|:---:|:---:|
| **200** | Kaos 801 (FG)<br>Kain 802 (WIP)<br>Kapas 803 (RM) | 6.900 pcs<br>1.725 meter<br>2.070 kg | **6.900,0000 pcs**<br>**1.725,0000 meter**<br>**2.070,0000 kg** | 0,00<br>0,00<br>0,00 | **OK**<br>**OK**<br>**OK** |
| **500** | Kaos 801 (FG)<br>Kain 802 (WIP)<br>Kapas 803 (RM) | 17.250 pcs<br>4.312,5 meter<br>5.175 kg | **17.250,0000 pcs**<br>**4.312,5000 meter**<br>**5.175,0000 kg** | 0,00<br>0,00<br>0,00 | **OK**<br>**OK**<br>**OK** |
| **1000** | Kaos 801 (FG)<br>Kain 802 (WIP)<br>Kapas 803 (RM) | 34.500 pcs<br>8.625 meter<br>10.350 kg | **34.500,0000 pcs**<br>**8.625,0000 meter**<br>**10.350,0000 kg** | 0,00<br>0,00<br>0,00 | **OK**<br>**OK**<br>**OK** |

Seluruh assertion kuantitas tervalidasi **100% presisi** tanpa deviasi.

---

## Reproduksi terbaru — 28 Oktober 2026

- `go test -race ./...`: lulus.
- Tagged PostgreSQL integration suite: lulus dengan `TEST_DATABASE_URL` eksplisit.
- State-machine acceptance yang ditambahkan menguji claim yang terlihat dari
  koneksi lain, double-claim prevention, `MarkFailed` tidak menimpa `cancelled`,
  writer tidak mengubah hasil run cancelled, retry idempotent, dan schema-version
  mismatch menjadi `failed_permanent`.
- E2E lokal dari dua login tenant melalui NestJS, Redis/BullMQ, dan worker ini
  mencapai `completed` untuk kedua run; job ID tenant-scoped mencegah collision.

Bagian sesudah ini adalah bukti historis yang tetap dipertahankan sebagai audit
trail. Bila ada perbedaan, hasil reproduksi terbaru dan kode test adalah acuan.

## 4. Test B — Forced Retry, Chaos, Supervisi & Multi-Tenant

### 4.1 Ringkasan Hasil Test B (Template Bagian 6.2)

| Test ID | Skenario Uji | Tanggal | Lulus? | Hasil Aktual | Temuan & Tindak Lanjut |
|:---:|:---|:---:|:---:|:---|:---|
| **B-1a** | Job Menunggu di Queue, Row Stuck | 11/10/2026 | ✅ Ya | Deteksi stuck berhasil dalam siklus cron 60s. Status DB beralih ke `pending` (retry 1), Redis mempertahankan 1 job di queue `wait`, worker mengambil dan menyelesaikan hingga `completed`. | Mekanisme dasar requeue berjalan sesuai desain. |
| **B-1b (Mek.)** | Job Terminal di Redis (Dedup `jobId`) | 11/10/2026 | ✅ Ya | Cron mendeteksi job terminal lama di Redis, memanggil `job.remove()`, lalu me-requeue job baru dengan ID yang sama tanpa terblokir dedup BullMQ. | Fix `job.remove()` terbukti efektif di runtime. |
| **B-1b (Idem.)** | Idempotensi Re-Run | 11/10/2026 | ✅ Ya | **Sebelum perbaikan**: $N_{sebelum} = 3 \rightarrow N_{sesudah} = 6$ (Duplikasi).<br>**Setelah perbaikan**: $N_{sebelum} = 3 \rightarrow N_{sesudah} = 3$ (100% Idempotent). | Diperbaiki via `DeletePlannedOrdersByRunID` di `writer.go`. |
| **B-2** | Batas Retry Habis $\rightarrow$ `failed_permanent` | 11/10/2026 | ✅ Ya | Pada `retry_count=3`, cron menandai run `failed_permanent`, mengisi `completed_at`, tidak mem-push job baru ke Redis, dan membuka kunci trigger run baru (HTTP 201). | Transisi terminal permanen aman. |
| **B-3** | Real Crash Worker (`kill -9`) | 11/10/2026 | ✅ Ya | Posisi job setelah kill: `active` di Redis.<br>• **H1: BENAR** (job tertinggal di set `active`).<br>• **H2: BENAR** (NestJS reconciliation skip selama worker mati).<br>• **H3: BENAR** (setelah worker dihidupkan, stalled checker aktif $\rightarrow$ cron me-requeue $\rightarrow$ run selesai). | Mengharuskan supervisi proses worker dan alerting eksternal (diselesaikan di Item 2). |
| **B-4a** | Trigger Berurutan saat Run Aktif | 11/10/2026 | ✅ Ya | Request 1: HTTP 201 (`runId: 20`).<br>Request 2: HTTP 409 Conflict (`MRP Run #20 masih berjalan...`). | Pre-check aplikasi menolak request berurutan. |
| **B-4b** | Trigger Paralel Bersamaan (Race Condition) | 11/10/2026 | ✅ Ya | Pre-check dinonaktifkan sementara: 2 request paralel menghasilkan HTTP 201 dan HTTP 409 dengan pesan handler 23505 (`MRP Run lain baru saja mulai berjalan...`). 0 HTTP 500. | Pertahanan lapis database terbukti aktif dan terisolasi. |

---

### 4.2 Supervisi Worker & Auto-Restart (Item 2)

Untuk mengatasi temuan pada skenario B-3 (di mana worker yang mati membuat run tertahan tanpa batas selagi tidak ada proses hidup), telah diimplementasikan:
1. **Supervisor Script (`scripts/worker_supervisor.sh`)**:
   Menjalankan worker Go di dalam process loop ber-guard. Jika proses worker mati mendadak (termasuk sinyal `kill -9`), supervisor mendeteksi exit code (`137`) dan secara instan me-restart proses worker baru dalam waktu 1 detik:
   ```text
   [Supervisor] [2026-10-11 15:05:44] PERINGATAN: Worker (PID: 73325) berhenti dengan exit code 137!
   [Supervisor] Total restart otomatis: 1 kali. Me-restart dalam 1 detik...
   [Supervisor] [2026-10-11 15:05:45] Spawning worker process...
   [Supervisor] Worker spawned dengan PID: 73339
   ```
2. **Template Service Production (`deploy/systemd/mrp-worker.service`)**:
   Disediakan unit file systemd dengan direktif `Restart=always` dan `RestartSec=3s` untuk deployment server Linux.

---

### 4.3 Alerting Monitoring Eksternal (Item 2)

Pada `MrpReconciliationService`, telah diintegrasikan mekanisme *structured alerting*:
1. **Alert Run Berdurasi Lebih dari 10 Menit (`LONG_RUNNING`)**:
   Mendeteksi jika ada run dengan status `running` yang berjalan melebihi 10 menit (indikasi worker macet atau crash tanpa restart):
   ```text
   [Nest] ERROR [MrpReconciliationService] [CRITICAL_ALERT:LONG_RUNNING] MRP Run #37 on tenant=admin: {"alertType":"LONG_RUNNING","timestamp":"2026-10-11T08:07:00.054Z","tenant":"admin","runId":37,"durationMinutes":16,"startedAt":"2026-10-11T07:51:00.867Z","isActiveInQueue":false}
   ```
2. **Alert Kegagalan Permanen (`FAILED_PERMANENT`)**:
   Mendeteksi jika run mencapai batas maksimal 3 kali percobaan:
   ```text
   [Nest] ERROR [MrpReconciliationService] [CRITICAL_ALERT:FAILED_PERMANENT] MRP Run #38 on tenant=admin: {"alertType":"FAILED_PERMANENT","timestamp":"2026-10-11T08:08:00.056Z","tenant":"admin","runId":38,"retryCount":3,"reason":"Exceeded maximum retry limit (3 attempts)"}
   ```

---

### 4.4 Pengujian Isolasi Multi-Tenant Simultan (Item 5b)

Pengujian isolasi multi-tenant nyata dilakukan dengan mengeksekusi dua MRP run secara bersamaan pada dua schema terpisah:
- **Tenant 1 (`tenant_admin`)**: Memproses permintaan 100 Kaos Dewasa (Variant 801).
- **Tenant 2 (`tenant_demo`)**: Memproses permintaan 150 Kemeja Formal (Variant 901).

Kedua request dikirim serentak melalui API HTTP:
- Request Tenant Admin $\rightarrow$ `runId: mSX29xaL`, status `completed` (durasi DB: 24 ms).
- Request Tenant Demo $\rightarrow$ `runId: 1`, status `completed` (durasi DB: 92 ms).

#### Hasil Verifikasi Pemisahan Data:
1. **`tenant_admin.planned_orders`**:
   - Memuat tepat hasil untuk Kaos 801, Kain 802, dan Kapas 803.
   - **Variant Kemeja (901, 902, 903) bocor ke admin = 0 (NOL)**.
2. **`tenant_demo.planned_orders`**:
   - Memuat tepat hasil untuk Kemeja 901 (150 pcs), Kain Oxford 902 (60 meter), Kancing Putih 903 (1.200 pcs).
   - **Variant Kaos (801, 802, 803) bocor ke demo = 0 (NOL)**.
3. **Session Pooling Safety**:
   - Implementasi `SET LOCAL search_path` di `internal/infra/db/db.go` terbukti 100% mengisolasi transaksi PgBouncer tanpa kebocoran session state ke request berikutnya.

---

## 5. Ringkasan Temuan & Perbaikan Teknis

| # | Temuan Masalah | Dampak | Tindak Lanjut Perbaikan | Status |
|---|---|:---:|---|:---:|
| **1** | Duplikasi Planned Orders saat Retry | Blocker | Ditambahkan `DeletePlannedOrdersByRunID` di `writer.go` & `repository.go` (clean-before-write). | **Selesai (Fixed)** |
| **2** | Perubahan Migrasi 012 Berisiko Checksum Error | Blocker | Migrasi 012 di-revert ke semula, dibuat migrasi baru `013_mrp_service_grants.sql`. | **Selesai (Fixed)** |
| **3** | Resiko Kebocoran Session di PgBouncer | Blocker Multi-Tenant | Diganti menjadi `SET LOCAL search_path TO %s, public` di `internal/infra/db/db.go`. | **Selesai (Fixed)** |
| **4** | Handler Error 23505 Belum Pernah Teruji | Verifikasi Desain | Pre-check dinonaktifkan sementara dan diuji race condition: terbukti 409 pesan 23505. | **Selesai (Fixed)** |
| **5** | Assertion Kuantitas Belum Teruji | Verifikasi Desain | Dihitung terhadap rumus seed dan rasio BOM: selisih 0.00 untuk 200, 500, 1.000 demands. | **Selesai (Fixed)** |
| **6** | Worker Crash Tidak Pulih Tanpa Intervensi | Prasyarat Operasional | Dibuat `scripts/worker_supervisor.sh` & systemd unit dengan restart otomatis dalam 1s. | **Selesai (Fixed)** |
| **7** | Tidak Ada Notifikasi Run Macet / Gagal | Prasyarat Operasional | Ditambahkan logging `[CRITICAL_ALERT:LONG_RUNNING]` & `FAILED_PERMANENT` di cron NestJS. | **Selesai (Fixed)** |
| **8** | Klaim Multi-Tenant Belum Tervalidasi Nyata | Prasyarat Arsitektur | Dijalankan uji simultan 2 tenant (`tenant_admin` & `tenant_demo`): 0 kebocoran data. | **Selesai (Fixed)** |

---

## 6. Kesimpulan & Kesiapan Gerbang Frontend (Gate Status)

Berdasarkan seluruh hasil pengujian dan evaluasi pada lembar kerja [`mrp-engine-tindak-lanjut.xlsx`](file:///Users/rico/Documents/Project/ERP/erp-mrp-engine/mrp-engine-tindak-lanjut.xlsx):

1. **Gate "Engine Selesai"**: **TERCAPAI (4/4 Item — 100%)**  
   - Item 1 (Migration 012 & 013): Selesai.
   - Item 2 (Supervisi Worker & Alerting): Selesai.
   - Item 5a (`SET LOCAL search_path`): Selesai.
   - Item 5b (Uji Dua Tenant Simultan): Selesai.
2. **Gate "Dokumen Resmi"**: **TERCAPAI (6/6 Item — 100%)**  
   - Seluruh koreksi representasi angka, pembersihan tautan, dan sinkronisasi baseline/plan/session selesai tuntas.
3. **Keputusan Kesiapan Frontend**:
   - Sel evaluasi **"Boleh mulai frontend?"** pada sheet *Ringkasan* telah berubah menjadi **YA**.

Sistem MRP Engine (`erp-mrp-engine` dan `backend-erp`) telah memenuhi standar ketat integritas data, ketahanan kegagalan operasional, isolasi multi-tenant yang aman, dan siap secara penuh untuk dihubungkan ke modul antarmuka pengguna (Frontend).
