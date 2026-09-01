package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"erp-mrp-engine/internal/logger"
)

func TestLogger_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWithWriter(&buf, "info", "json")

	logWithContext := log.WithJobContext(123, "tenant_companyabc")
	logWithContext.InfoContext(context.Background(), "calculating BOM", "total_demands", 42)

	output := buf.String()
	if output == "" {
		t.Fatal("expected log output, got empty string")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("failed to parse JSON log output: %v", err)
	}

	if parsed["msg"] != "calculating BOM" {
		t.Errorf("expected msg 'calculating BOM', got %v", parsed["msg"])
	}
	if parsed["level"] != "INFO" {
		t.Errorf("expected level 'INFO', got %v", parsed["level"])
	}
	if parsed["mrp_run_id"] != float64(123) {
		t.Errorf("expected mrp_run_id 123, got %v", parsed["mrp_run_id"])
	}
	if parsed["tenant_schema"] != "tenant_companyabc" {
		t.Errorf("expected tenant_schema 'tenant_companyabc', got %v", parsed["tenant_schema"])
	}
	if parsed["total_demands"] != float64(42) {
		t.Errorf("expected total_demands 42, got %v", parsed["total_demands"])
	}
}

func TestLogger_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWithWriter(&buf, "debug", "text")

	log.WithRunID(456).DebugContext(context.Background(), "debug trace event")

	output := buf.String()
	if !strings.Contains(output, "level=DEBUG") {
		t.Errorf("expected level=DEBUG in output, got: %s", output)
	}
	if !strings.Contains(output, "msg=\"debug trace event\"") && !strings.Contains(output, "msg=debug trace event") {
		t.Errorf("expected message in output, got: %s", output)
	}
	if !strings.Contains(output, "mrp_run_id=456") {
		t.Errorf("expected mrp_run_id=456 in output, got: %s", output)
	}
}

func TestLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWithWriter(&buf, "warn", "json")

	log.Info("this should be ignored")
	if buf.Len() > 0 {
		t.Errorf("expected no output for info level when set to warn, got: %s", buf.String())
	}

	log.Warn("this warning should appear")
	if buf.Len() == 0 {
		t.Error("expected warning to be logged, but buffer is empty")
	}
}
