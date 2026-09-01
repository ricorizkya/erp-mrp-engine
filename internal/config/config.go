// Package config menyediakan konfigurasi runtime untuk MRP Engine.
// Konfigurasi dimuat dari environment variables dan opsional file .env.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Sentinel errors untuk validasi konfigurasi.
var (
	ErrMissingDatabaseURL = errors.New("missing required environment variable: MRP_DATABASE_URL or DATABASE_URL")
	ErrMissingRedisAddr   = errors.New("missing required environment variable: REDIS_ADDR or REDIS_URL")
)

// Config merepresentasikan seluruh konfigurasi aplikasi mrp-engine.
type Config struct {
	DatabaseURL           string
	RedisAddr             string
	QueueName             string
	WorkerConcurrency     int
	JobTimeoutMinutes     int
	MaxBomDepth           int
	DefaultLeadTimeDays   int
	DefaultProductionDays int
	MinQuantity           float64
	RetryAttempts         int
	LogLevel              string
	LogFormat             string
	AppEnv                string
	DBMaxConns            int
	DBMinConns            int
	DBConnTimeoutSeconds  int
}

// Load memuat konfigurasi dari environment variables.
// Jika file .env ada di working directory, nilai di dalamnya akan dimuat terlebih dahulu
// tanpa menimpa environment variables yang sudah ada.
func Load() (*Config, error) {
	// Abaikan error jika .env tidak ditemukan (misal di environment production/docker)
	_ = godotenv.Load()

	dbURL := getEnvWithFallback("MRP_DATABASE_URL", "DATABASE_URL")
	if dbURL == "" {
		return nil, ErrMissingDatabaseURL
	}

	redisAddr := getEnvWithFallback("REDIS_ADDR", "REDIS_URL")
	if redisAddr == "" {
		return nil, ErrMissingRedisAddr
	}

	cfg := &Config{
		DatabaseURL:           dbURL,
		RedisAddr:             redisAddr,
		QueueName:             getEnvString("REDIS_QUEUE_NAME", "mrp-calculation"),
		WorkerConcurrency:     getEnvInt("MRP_WORKER_CONCURRENCY", 5),
		JobTimeoutMinutes:     getEnvInt("MRP_JOB_TIMEOUT_MINUTES", 10),
		MaxBomDepth:           getEnvInt("MRP_MAX_BOM_DEPTH", 10),
		DefaultLeadTimeDays:   getEnvInt("MRP_DEFAULT_LEAD_TIME_DAYS", 7),
		DefaultProductionDays: getEnvInt("MRP_DEFAULT_PRODUCTION_DAYS", 3),
		MinQuantity:           getEnvFloat("MRP_MIN_QUANTITY", 0.001),
		RetryAttempts:         getEnvInt("MRP_RETRY_ATTEMPTS", 3),
		LogLevel:              getEnvString("LOG_LEVEL", "info"),
		LogFormat:             getEnvString("LOG_FORMAT", "json"),
		AppEnv:                getEnvString("APP_ENV", "development"),
		DBMaxConns:            getEnvInt("DB_MAX_CONNS", 10),
		DBMinConns:            getEnvInt("DB_MIN_CONNS", 2),
		DBConnTimeoutSeconds:  getEnvInt("DB_CONN_TIMEOUT_SECONDS", 10),
	}

	return cfg, nil
}

func getEnvWithFallback(primaryKey, fallbackKey string) string {
	if val := os.Getenv(primaryKey); val != "" {
		return val
	}
	return os.Getenv(fallbackKey)
}

func getEnvString(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	valStr := os.Getenv(key)
	if valStr == "" {
		return fallback
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return fallback
	}
	return val
}

func getEnvFloat(key string, fallback float64) float64 {
	valStr := os.Getenv(key)
	if valStr == "" {
		return fallback
	}
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return fallback
	}
	return val
}

// String menampilkan ringkasan konfigurasi aman (menyembunyikan credential DB).
func (c *Config) String() string {
	return fmt.Sprintf(
		"Config{Queue: %s, Concurrency: %d, MaxBomDepth: %d, LogLevel: %s, LogFormat: %s, AppEnv: %s}",
		c.QueueName, c.WorkerConcurrency, c.MaxBomDepth, c.LogLevel, c.LogFormat, c.AppEnv,
	)
}
