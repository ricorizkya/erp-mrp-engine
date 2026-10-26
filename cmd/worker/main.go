package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	gobullmq "go.codycody31.dev/gobullmq"

	"erp-mrp-engine/internal/config"
	"erp-mrp-engine/internal/domain"
	infradb "erp-mrp-engine/internal/infra/db"
	"erp-mrp-engine/internal/infra/mrp"
	"erp-mrp-engine/internal/logger"
)

func main() {
	// 1. Muat konfigurasi aplikasi
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// 2. Inisialisasi structured logger
	log := logger.New(cfg.LogLevel, cfg.LogFormat)
	log.Info("starting MRP calculation worker",
		"queue", cfg.QueueName,
		"concurrency", cfg.WorkerConcurrency,
		"max_bom_depth", cfg.MaxBomDepth,
		"app_env", cfg.AppEnv,
	)

	// 3. Konteks aplikasi dengan signal cancel untuk graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// 4. Inisialisasi PostgreSQL Connection Pool (dengan retry koneksi saat startup)
	pool, err := initDatabasePool(ctx, cfg.DatabaseURL, cfg.RetryAttempts, log)
	if err != nil {
		log.Error("failed to connect to database pool", "error", err.Error())
		os.Exit(1)
	}
	defer pool.Close()

	// 5. Inisialisasi Redis Client
	var redisClient *redis.Client
	if opt, err := redis.ParseURL(cfg.RedisAddr); err == nil {
		redisClient = redis.NewClient(opt)
	} else {
		redisClient = redis.NewClient(&redis.Options{
			Addr: cfg.RedisAddr,
		})
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Warn("error closing redis client", "error", err.Error())
		}
	}()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Error("failed to ping redis", "error", err.Error(), "addr", cfg.RedisAddr)
		os.Exit(1)
	}

	// 6. Inisialisasi MRP Engine Orchestrator
	engine := mrp.NewEngine(pool, cfg, log)

	// 7. Mulai HTTP Health Check Server di background (untuk PM2 / container monitoring)
	startHealthServer(ctx, pool, redisClient, log)

	// 8. Inisialisasi BullMQ Worker
	worker, err := gobullmq.NewWorker(
		cfg.QueueName,
		redisClient,
		func(jobCtx context.Context, job *gobullmq.Job[domain.MrpJob]) (any, error) {
			jobData := job.Data()
			if err := engine.Run(jobCtx, &jobData); err != nil {
				return nil, err
			}
			return nil, nil
		},
		&gobullmq.WorkerOptions{
			Concurrency:     cfg.WorkerConcurrency,
			StalledInterval: 30 * time.Second,
		},
	)
	if err != nil {
		log.Error("failed to initialize bullmq worker", "error", err.Error())
		os.Exit(1)
	}

	// Jalankan worker di goroutine
	workerErrChan := make(chan error, 1)
	go func() {
		log.Info("mrp worker started", "queue", cfg.QueueName, "concurrency", cfg.WorkerConcurrency)
		if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			workerErrChan <- err
		}
		close(workerErrChan)
	}()

	// 9. Tunggu sinyal shutdown atau worker error
	select {
	case <-ctx.Done():
		log.Info("shutdown signal received, waiting for in-progress jobs to finish...")
	case err := <-workerErrChan:
		if err != nil {
			log.Error("worker stopped unexpectedly", "error", err.Error())
		}
	}

	// Graceful wait
	worker.Wait()
	log.Info("mrp worker shutdown complete")
}

func initDatabasePool(
	ctx context.Context,
	dsn string,
	maxAttempts int,
	log *logger.Logger,
) (*infradb.Pool, error) {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pool, err := infradb.NewPool(ctx, dsn)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
			lastErr = err
		} else {
			lastErr = err
		}

		log.Warn("database connection attempt failed, retrying...",
			"attempt", attempt,
			"max_attempts", maxAttempts,
			"error", lastErr.Error(),
		)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}

	return nil, fmt.Errorf("could not connect to database after %d attempts: %w", maxAttempts, lastErr)
}

func startHealthServer(
	ctx context.Context,
	pool *infradb.Pool,
	redisClient *redis.Client,
	log *logger.Logger,
) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		dbStatus := "ok"
		if err := pool.Ping(r.Context()); err != nil {
			dbStatus = "down"
		}

		redisStatus := "ok"
		if err := redisClient.Ping(r.Context()).Err(); err != nil {
			redisStatus = "down"
		}

		status := "ok"
		httpStatus := http.StatusOK
		if dbStatus != "ok" || redisStatus != "ok" {
			status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}

		resp := map[string]string{
			"status": status,
			"db":     dbStatus,
			"redis":  redisStatus,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		_ = json.NewEncoder(w).Encode(resp)
	})

	server := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("health check server stopped", "error", err.Error())
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
}
