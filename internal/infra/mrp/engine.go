package mrp

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"erp-mrp-engine/internal/config"
	"erp-mrp-engine/internal/domain"
	"erp-mrp-engine/internal/infra/db"
	"erp-mrp-engine/internal/logger"
)

// Engine adalah top-level orchestrator yang menangani eksekusi job MRP per tenant.
type Engine struct {
	pool       *pgxpool.Pool
	loader     *Loader
	calculator *domain.Calculator
	writer     *Writer
	logger     *logger.Logger
	cfg        *config.Config
}

// NewEngine membuat instance Engine baru.
func NewEngine(pool *pgxpool.Pool, cfg *config.Config, log *logger.Logger) *Engine {
	exploder := domain.NewBomExploder(nil, cfg.MaxBomDepth)
	scheduler := domain.NewScheduler(cfg.DefaultLeadTimeDays, cfg.DefaultProductionDays)
	calculator := domain.NewCalculator(exploder, scheduler, cfg.MinQuantity)

	return &Engine{
		pool:       pool,
		loader:     NewLoader(pool, log),
		calculator: calculator,
		writer:     NewWriter(pool, log),
		logger:     log,
		cfg:        cfg,
	}
}

// Run menjalankan seluruh alur kalkulasi MRP untuk suatu job.
func (e *Engine) Run(ctx context.Context, job *domain.MrpJob) (err error) {
	log := e.logger.WithJobContext(job.RunID, job.TenantSchema)
	log.InfoContext(ctx, "starting mrp calculation job")

	// 1. Validasi versi schema job contract
	if job.SchemaVersion != domain.SupportedSchemaVersion {
		errMsg := fmt.Sprintf("unsupported job schema version: %d (expected %d)", job.SchemaVersion, domain.SupportedSchemaVersion)
		log.ErrorContext(ctx, errMsg)

		_ = db.WithTenantSchema(ctx, e.pool, job.TenantSchema, func(tx pgx.Tx) error {
			return db.MarkFailedPermanent(ctx, tx, job.RunID, errMsg)
		})
		return fmt.Errorf("%s", errMsg)
	}

	startTime := time.Now()

	// 2. Proteksi terhadap panic
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			panicMsg := fmt.Sprintf("panic in mrp calculation: %v\n%s", r, stack)
			log.ErrorContext(ctx, "unhandled panic during mrp run", "error", panicMsg)

			_ = db.WithTenantSchema(ctx, e.pool, job.TenantSchema, func(tx pgx.Tx) error {
				return db.MarkFailed(ctx, tx, job.RunID, fmt.Sprintf("panic: %v", r))
			})
			err = fmt.Errorf("panic in mrp run: %v", r)
		}
	}()

	// 3. Eksekusi seluruh proses di dalam transaksi tenant yang terisolasi
	err = db.WithTenantSchema(ctx, e.pool, job.TenantSchema, func(tx pgx.Tx) error {
		// A. Tandai 'running'
		isAcquired, markErr := db.MarkRunning(ctx, tx, job.RunID, job.SchemaVersion)
		if markErr != nil {
			return fmt.Errorf("mark running: %w", markErr)
		}
		if !isAcquired {
			log.WarnContext(ctx, "job skipped: status was not pending or already claimed by another worker")
			return nil
		}

		// B. Load input data
		inputData, loadErr := e.loader.LoadAll(ctx, tx, job.RunID, job.TenantSchema, job.PlanFrom, job.PlanTo)
		if loadErr != nil {
			return fmt.Errorf("load input data: %w", loadErr)
		}

		// Inisialisasi ulang exploder dengan input data yang baru diload
		exploder := domain.NewBomExploder(inputData, e.cfg.MaxBomDepth)
		scheduler := domain.NewScheduler(e.cfg.DefaultLeadTimeDays, e.cfg.DefaultProductionDays)
		calc := domain.NewCalculator(exploder, scheduler, e.cfg.MinQuantity)

		// C. Kalkulasi Net Requirement & BOM Explosion
		calcResult, calcErr := calc.Calculate(inputData)
		if calcErr != nil {
			return fmt.Errorf("calculate mrp: %w", calcErr)
		}

		// D. Simpan hasil kalkulasi dan update status ke 'completed'
		durationMs := time.Since(startTime).Milliseconds()
		if writeErr := e.writer.Write(ctx, tx, job.RunID, job.TenantSchema, calcResult, durationMs); writeErr != nil {
			return fmt.Errorf("write results: %w", writeErr)
		}

		return nil
	})

	if err != nil {
		log.ErrorContext(ctx, "mrp calculation failed", "error", err.Error())
		_ = db.WithTenantSchema(ctx, e.pool, job.TenantSchema, func(tx pgx.Tx) error {
			return db.MarkFailed(ctx, tx, job.RunID, err.Error())
		})
		return err
	}

	log.InfoContext(ctx, "mrp calculation job finished successfully", "duration_ms", time.Since(startTime).Milliseconds())
	return nil
}
