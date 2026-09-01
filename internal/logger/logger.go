// Package logger menyediakan structured logging wrapper menggunakan log/slog.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Logger membungkus *slog.Logger dengan helper kontekstual khusus domain MRP.
type Logger struct {
	*slog.Logger
}

// New menginisialisasi Logger baru berdasarkan level dan format (json / text).
func New(levelStr, formatStr string) *Logger {
	return NewWithWriter(os.Stdout, levelStr, formatStr)
}

// NewWithWriter membuat Logger baru dengan output writer kustom (berguna untuk testing).
func NewWithWriter(w io.Writer, levelStr, formatStr string) *Logger {
	level := parseLevel(levelStr)

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if strings.ToLower(formatStr) == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	return &Logger{
		Logger: slog.New(handler),
	}
}

// WithRunID menambahkan atribut mrp_run_id ke logger.
func (l *Logger) WithRunID(runID int64) *Logger {
	return &Logger{
		Logger: l.Logger.With(slog.Int64("mrp_run_id", runID)),
	}
}

// WithTenant menambahkan atribut tenant_schema ke logger.
func (l *Logger) WithTenant(tenantSchema string) *Logger {
	return &Logger{
		Logger: l.Logger.With(slog.String("tenant_schema", tenantSchema)),
	}
}

// WithContextAttrs menambahkan run_id dan tenant_schema sekaligus jika relevan.
func (l *Logger) WithJobContext(runID int64, tenantSchema string) *Logger {
	return &Logger{
		Logger: l.Logger.With(
			slog.Int64("mrp_run_id", runID),
			slog.String("tenant_schema", tenantSchema),
		),
	}
}

// InfoContext membungkus slog.InfoContext.
func (l *Logger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.Logger.InfoContext(ctx, msg, args...)
}

// ErrorContext membungkus slog.ErrorContext.
func (l *Logger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.Logger.ErrorContext(ctx, msg, args...)
}

// WarnContext membungkus slog.WarnContext.
func (l *Logger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.Logger.WarnContext(ctx, msg, args...)
}

// DebugContext membungkus slog.DebugContext.
func (l *Logger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.Logger.DebugContext(ctx, msg, args...)
}

func parseLevel(levelStr string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(levelStr)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
