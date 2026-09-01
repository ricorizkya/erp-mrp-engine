package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// MarkRunning — operasi PERTAMA dan paling ringan setelah job di-pickup,
// SEBELUM kalkulasi berat mulai. WHERE status='pending' (bukan cuma
// WHERE id=$1) sebagai defense terhadap race condition kalau suatu
// saat ada >1 Go worker instance — lihat spec section 4.
//
// Kalau RowsAffected() == 0, berarti run ini sudah dipegang worker lain
// atau statusnya bukan 'pending' lagi — caller HARUS treat ini sebagai
// sinyal untuk skip job, bukan lanjut proses.
func MarkRunning(ctx context.Context, tx pgx.Tx, runID int64, schemaVersion int) (bool, error) {
	tag, err := tx.Exec(ctx,
		`UPDATE mrp_runs
		 SET status = 'running', started_at = NOW(), job_schema_version = $2
		 WHERE id = $1 AND status = 'pending'`,
		runID, schemaVersion,
	)
	if err != nil {
		return false, fmt.Errorf("mark running: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkFailedPermanent — dipakai untuk schemaVersion mismatch (skip retry
// sepenuhnya, lihat spec section 6.2) maupun setelah retry_count habis
// (dipanggil dari reconciliation NestJS, bukan dari sini — fungsi ini
// untuk kasus Go sendiri yang menentukan gagal permanen, misal version
// mismatch terdeteksi saat parse job).
func MarkFailedPermanent(ctx context.Context, tx pgx.Tx, runID int64, reason string) error {
	_, err := tx.Exec(ctx,
		`UPDATE mrp_runs
		 SET status = 'failed_permanent', error_message = $2, completed_at = NOW()
		 WHERE id = $1`,
		runID, reason,
	)
	if err != nil {
		return fmt.Errorf("mark failed_permanent: %w", err)
	}
	return nil
}

// CompleteRun — update status final + insert planned_orders dalam SATU
// transaksi (caller wajib pass tx yang sama, commit/rollback di level
// caller). Ini enforce keputusan spec section 4: status dan hasil tidak
// boleh jadi dua operasi terpisah.
//
// STUB: insert planned_orders belum diimplementasi (exploder masih
// placeholder di internal/domain/bom_exploder.go). Fungsi ini untuk
// sekarang cuma update status + total counter ke 0, supaya pipeline
// end-to-end (job masuk -> status berubah -> job selesai) bisa divalidasi
// dulu sebelum logic kalkulasi asli ditulis.
func CompleteRun(ctx context.Context, tx pgx.Tx, runID int64, totalProduction, totalPurchase int) error {
	_, err := tx.Exec(ctx,
		`UPDATE mrp_runs
		 SET status = 'completed',
		     completed_at = NOW(),
		     total_planned_production = $2,
		     total_planned_purchase = $3
		 WHERE id = $1`,
		runID, totalProduction, totalPurchase,
	)
	if err != nil {
		return fmt.Errorf("complete run: %w", err)
	}
	// TODO: insert ke planned_orders + planned_order_demands di sini,
	// dalam transaksi yang sama, setelah exploder logic asli ada.
	return nil
}

// MarkFailed — kegagalan transient (bukan permanent), retry masih
// mungkin lewat reconciliation NestJS.
func MarkFailed(ctx context.Context, tx pgx.Tx, runID int64, reason string) error {
	_, err := tx.Exec(ctx,
		`UPDATE mrp_runs
		 SET status = 'failed', error_message = $2, completed_at = NOW()
		 WHERE id = $1`,
		runID, reason,
	)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	return nil
}
