module erp-mrp-engine

go 1.25.0

require (
	github.com/jackc/pgx/v5 v5.10.0
	github.com/redis/go-redis/v9 v9.22.0
	// PERINGATAN: gobullmq eksplisit menyatakan kompatibel dengan
	// BullMQ v4.12.2 — BUKAN v5 yang jadi target NestJS kalian, dan
	// JELAS bukan v6 (rilis 30 Juli 2026, ubah total ke pluggable
	// backend). Wajib spike test end-to-end sebelum dipercaya untuk
	// production: push job dari NestJS BullMQ, verifikasi job.isActive()
	// di sisi Node benar-benar true saat Go sedang proses job itu.
	// Kalau gagal, reconciliation design di spec (section 5) butuh
	// redesign — jangan asumsikan lock/stalled-detection BullMQ
	// otomatis kompatibel.
	go.codycody31.dev/gobullmq v1.1.0
)

require github.com/joho/godotenv v1.5.1

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)
