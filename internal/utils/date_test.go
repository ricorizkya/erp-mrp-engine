package utils_test

import (
	"testing"
	"time"

	"erp-mrp-engine/internal/utils"
)

func TestSubtractDays(t *testing.T) {
	base := time.Date(2026, time.March, 15, 10, 0, 0, 0, time.UTC)
	got := utils.SubtractDays(base, 5)
	expected := time.Date(2026, time.March, 10, 10, 0, 0, 0, time.UTC)

	if !got.Equal(expected) {
		t.Errorf("SubtractDays(2026-03-15, 5) = %v, want %v", got, expected)
	}
}

func TestAddDays(t *testing.T) {
	base := time.Date(2026, time.February, 27, 0, 0, 0, 0, time.UTC)
	got := utils.AddDays(base, 2)
	expected := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)

	if !got.Equal(expected) {
		t.Errorf("AddDays(2026-02-27, 2) = %v, want %v", got, expected)
	}
}

func TestBeginningOfDay(t *testing.T) {
	input := time.Date(2026, time.April, 1, 15, 30, 45, 12345, time.UTC)
	got := utils.BeginningOfDay(input)
	expected := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)

	if !got.Equal(expected) {
		t.Errorf("BeginningOfDay(%v) = %v, want %v", input, got, expected)
	}
}

func TestIsBeforeToday(t *testing.T) {
	yesterday := time.Now().AddDate(0, 0, -1)
	tomorrow := time.Now().AddDate(0, 0, 1)
	today := time.Now()

	if !utils.IsBeforeToday(yesterday) {
		t.Errorf("expected yesterday (%v) to be before today", yesterday)
	}

	if utils.IsBeforeToday(tomorrow) {
		t.Errorf("expected tomorrow (%v) NOT to be before today", tomorrow)
	}

	if utils.IsBeforeToday(today) {
		t.Errorf("expected today (%v) NOT to be before today", today)
	}
}

func TestMaxTime_MinTime(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	if got := utils.MaxTime(t1, t2); !got.Equal(t2) {
		t.Errorf("MaxTime(%v, %v) = %v, want %v", t1, t2, got, t2)
	}

	if got := utils.MinTime(t1, t2); !got.Equal(t1) {
		t.Errorf("MinTime(%v, %v) = %v, want %v", t1, t2, got, t1)
	}
}
