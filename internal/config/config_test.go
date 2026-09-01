package config_test

import (
	"errors"
	"testing"

	"erp-mrp-engine/internal/config"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		envSetup    func(t *testing.T)
		wantErr     error
		verifyField func(t *testing.T, cfg *config.Config)
	}{
		{
			name: "missing database URL",
			envSetup: func(t *testing.T) {
				t.Setenv("MRP_DATABASE_URL", "")
				t.Setenv("DATABASE_URL", "")
				t.Setenv("REDIS_ADDR", "localhost:6379")
			},
			wantErr: config.ErrMissingDatabaseURL,
		},
		{
			name: "missing redis address",
			envSetup: func(t *testing.T) {
				t.Setenv("MRP_DATABASE_URL", "postgres://user:pass@localhost:5432/db")
				t.Setenv("REDIS_ADDR", "")
				t.Setenv("REDIS_URL", "")
			},
			wantErr: config.ErrMissingRedisAddr,
		},
		{
			name: "valid with defaults",
			envSetup: func(t *testing.T) {
				t.Setenv("MRP_DATABASE_URL", "postgres://user:pass@localhost:5432/db")
				t.Setenv("REDIS_ADDR", "localhost:6379")
				t.Setenv("REDIS_QUEUE_NAME", "")
				t.Setenv("MRP_WORKER_CONCURRENCY", "")
			},
			wantErr: nil,
			verifyField: func(t *testing.T, cfg *config.Config) {
				if cfg.QueueName != "mrp-calculation" {
					t.Errorf("expected QueueName 'mrp-calculation', got %q", cfg.QueueName)
				}
				if cfg.WorkerConcurrency != 5 {
					t.Errorf("expected WorkerConcurrency 5, got %d", cfg.WorkerConcurrency)
				}
				if cfg.MaxBomDepth != 10 {
					t.Errorf("expected MaxBomDepth 10, got %d", cfg.MaxBomDepth)
				}
				if cfg.DefaultLeadTimeDays != 7 {
					t.Errorf("expected DefaultLeadTimeDays 7, got %d", cfg.DefaultLeadTimeDays)
				}
				if cfg.MinQuantity != 0.001 {
					t.Errorf("expected MinQuantity 0.001, got %f", cfg.MinQuantity)
				}
			},
		},
		{
			name: "valid with custom values and fallback env names",
			envSetup: func(t *testing.T) {
				t.Setenv("MRP_DATABASE_URL", "")
				t.Setenv("DATABASE_URL", "postgres://fallback:pass@localhost:5432/db")
				t.Setenv("REDIS_ADDR", "")
				t.Setenv("REDIS_URL", "redis://fallback:6379")
				t.Setenv("REDIS_QUEUE_NAME", "custom-queue")
				t.Setenv("MRP_WORKER_CONCURRENCY", "10")
				t.Setenv("LOG_LEVEL", "debug")
				t.Setenv("LOG_FORMAT", "text")
			},
			wantErr: nil,
			verifyField: func(t *testing.T, cfg *config.Config) {
				if cfg.DatabaseURL != "postgres://fallback:pass@localhost:5432/db" {
					t.Errorf("expected DatabaseURL from fallback, got %q", cfg.DatabaseURL)
				}
				if cfg.RedisAddr != "redis://fallback:6379" {
					t.Errorf("expected RedisAddr from fallback, got %q", cfg.RedisAddr)
				}
				if cfg.QueueName != "custom-queue" {
					t.Errorf("expected QueueName 'custom-queue', got %q", cfg.QueueName)
				}
				if cfg.WorkerConcurrency != 10 {
					t.Errorf("expected WorkerConcurrency 10, got %d", cfg.WorkerConcurrency)
				}
				if cfg.LogLevel != "debug" {
					t.Errorf("expected LogLevel 'debug', got %q", cfg.LogLevel)
				}
				if cfg.LogFormat != "text" {
					t.Errorf("expected LogFormat 'text', got %q", cfg.LogFormat)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.envSetup(t)
			cfg, err := config.Load()

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.verifyField != nil {
				tt.verifyField(t, cfg)
			}
		})
	}
}
