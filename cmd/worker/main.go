package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	gobullmq "go.codycody31.dev/gobullmq"

	"erp-mrp-engine/internal/domain"
	infradb "erp-mrp-engine/internal/infra/db"
)

// ============================================================
// STUB WORKER — untuk validasi end-to-end kontrak job, BUKAN
// implementasi MRP calculation asli. Lihat internal/domain/bom_exploder.go
// untuk status logic kalkulasi (masih placeholder).
//
// PERINGATAN KOMPATIBILITAS: gobullmq target BullMQ v4.12.2.
// NestJS project ini rencananya pakai BullMQ v5.x (pin eksplisit,
// hindari v6 yang baru rilis 30 Juli 2026 dengan pluggable backend
// yang mengubah internal signifikan). WAJIB spike test sebelum
// dipercaya: pastikan job.isActive() di sisi Node NestJS benar-benar
// true selagi Go pegang job ini — kalau tidak, reconciliation design
// (cek BullMQ sebelum retry) tidak valid dan perlu didesain ulang.
// ============================================================

type Config struct {
	DatabaseURL string
	RedisAddr   string
	RedisPass   string
	RedisDB     int
	QueueName   string
	Concurrency int
}

func loadConfig() Config {
	if err := godotenv.Load(); err != nil {
		log.Println("info: file .env tidak ditemukan atau tidak bisa dimuat, menggunakan environment variable sistem")
	}

	// 1. Database URL
	dsn := os.Getenv("MRP_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		host := getEnv("DB_HOST", "localhost")
		port := getEnv("DB_PORT", "5432")
		user := getEnv("DB_USER", "postgres")
		pass := os.Getenv("DB_PASSWORD")
		name := getEnv("DB_NAME", "postgres")
		ssl := getEnv("DB_SSLMODE", "disable")

		if pass != "" {
			dsn = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, name, ssl)
		} else {
			dsn = fmt.Sprintf("postgres://%s@%s:%s/%s?sslmode=%s", user, host, port, name, ssl)
		}
	}
	if dsn == "" {
		log.Fatal("env MRP_DATABASE_URL atau DATABASE_URL wajib di-set")
	}

	// 2. Redis Configuration
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		if u := os.Getenv("REDIS_URL"); u != "" {
			redisAddr = strings.TrimPrefix(u, "redis://")
		} else {
			host := getEnv("REDIS_HOST", "localhost")
			port := getEnv("REDIS_PORT", "6379")
			redisAddr = fmt.Sprintf("%s:%s", host, port)
		}
	}

	redisPass := os.Getenv("REDIS_PASSWORD")
	redisDB, _ := strconv.Atoi(getEnv("REDIS_DB", "0"))

	// 3. Queue Name & Concurrency
	queueName := getEnv("QUEUE_NAME", "mrp-calculation")
	concurrency, _ := strconv.Atoi(getEnv("WORKER_CONCURRENCY", "1"))
	if concurrency <= 0 {
		concurrency = 1
	}

	return Config{
		DatabaseURL: dsn,
		RedisAddr:   redisAddr,
		RedisPass:   redisPass,
		RedisDB:     redisDB,
		QueueName:   queueName,
		Concurrency: concurrency,
	}
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func main() {
	ctx := context.Background()

	cfg := loadConfig()

	pool, err := infradb.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db pool: %v", err)
	}
	defer pool.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPass,
		DB:       cfg.RedisDB,
	})

	worker, err := gobullmq.NewWorker(
		cfg.QueueName,
		redisClient,
		func(ctx context.Context, job *gobullmq.Job[domain.MrpJob]) (any, error) {
			return processJob(ctx, pool, job.Data())
		},
		&gobullmq.WorkerOptions{
			Concurrency:     cfg.Concurrency,
			StalledInterval: 30 * time.Second,
		},
	)
	if err != nil {
		log.Fatalf("create worker: %v", err)
	}

	log.Printf("mrp worker started (queue: %s, concurrency: %d)", cfg.QueueName, cfg.Concurrency)
	if err := worker.Run(ctx); err != nil {
		log.Fatalf("run worker: %v", err)
	}
	worker.Wait()
}

func processJob(ctx context.Context, pool *pgxpool.Pool, job domain.MrpJob) (any, error) {

	// Version check — reject + failed_permanent langsung, skip retry,
	// sesuai keputusan spec section 6.2
	if job.SchemaVersion != domain.SupportedSchemaVersion {
		err := infradb.WithTenantSchema(ctx, pool, job.TenantSchema, func(tx pgx.Tx) error {
			return infradb.MarkFailedPermanent(ctx, tx, job.RunID,
				"schemaVersion mismatch: got "+strconv.Itoa(job.SchemaVersion)+", supported "+strconv.Itoa(domain.SupportedSchemaVersion))
		})
		if err != nil {
			log.Printf("failed to mark failed_permanent for version mismatch, runId=%d: %v", job.RunID, err)
		}
		return nil, nil // job dianggap "selesai" dari sisi BullMQ, tidak perlu di-retry BullMQ (attempts:1 sudah di-set NestJS)
	}

	// Mark running — operasi pertama, paling ringan
	var picked bool
	err := infradb.WithTenantSchema(ctx, pool, job.TenantSchema, func(tx pgx.Tx) error {
		var e error
		picked, e = infradb.MarkRunning(ctx, tx, job.RunID, job.SchemaVersion)
		return e
	})
	if err != nil {
		return nil, err
	}
	if !picked {
		// Run sudah dipegang worker lain atau sudah selesai — skip,
		// jangan proses ulang. Lihat catatan race condition di repository.go
		log.Printf("runId=%d sudah tidak berstatus pending, skip", job.RunID)
		return nil, nil
	}

	// STUB: kalkulasi MRP sungguhan belum ada. Sleep pendek untuk
	// simulasi kerja, lalu langsung complete dengan hasil kosong —
	// tujuannya cuma validasi pipeline job -> status transition -> commit.
	time.Sleep(2 * time.Second)

	err = infradb.WithTenantSchema(ctx, pool, job.TenantSchema, func(tx pgx.Tx) error {
		return infradb.CompleteRun(ctx, tx, job.RunID, 0, 0)
	})
	if err != nil {
		// Kalkulasi (stub) berhasil tapi commit gagal — treat sebagai
		// failed (transient), reconciliation NestJS yang urus retry
		_ = infradb.WithTenantSchema(ctx, pool, job.TenantSchema, func(tx pgx.Tx) error {
			return infradb.MarkFailed(ctx, tx, job.RunID, err.Error())
		})
		return nil, err
	}

	return nil, nil
}

