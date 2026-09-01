package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool membuat connection pool ke Postgres lewat PgBouncer.
//
// WAJIB: QueryExecMode di-set ke SimpleProtocol. PgBouncer transaction
// mode mengembalikan koneksi fisik ke pool antar transaksi — pgx
// prepared statement cache per-koneksi bisa menyebabkan bug intermiten
// kalau default extended protocol dipakai. Ini BUKAN opsional, sudah
// diputuskan eksplisit di spec section 2.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	return pool, nil
}

// WithTenantSchema wrap sebuah fungsi supaya berjalan dalam transaksi
// yang sudah di-set search_path ke schema tenant. Ini replikasi manual
// dari TenantInterceptor di NestJS — Go tidak punya interceptor pattern
// setara, jadi wajib eksplisit dipanggil di setiap operasi DB.
func WithTenantSchema(ctx context.Context, pool *pgxpool.Pool, tenantSchema string, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op kalau sudah di-commit

	// search_path pakai format() bukan parameterized query karena
	// SET tidak mendukung placeholder ($1) di PostgreSQL. Nama schema
	// datang dari job contract (tenantSchema field), BUKAN dari input
	// user langsung — tapi tetap validasi format sebelum sampai sini
	// (lihat validasi regex ^tenant_[a-z0-9_]+$ di schema JSON) untuk
	// mencegah SQL injection lewat schema name.
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET search_path TO %s, public", pgx.Identifier{tenantSchema}.Sanitize())); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
