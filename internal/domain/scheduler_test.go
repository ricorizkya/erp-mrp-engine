package domain_test

import (
	"testing"
	"time"

	"erp-mrp-engine/internal/domain"
	"erp-mrp-engine/internal/utils"
)

func TestSchedule_Normal(t *testing.T) {
	scheduler := domain.NewScheduler(7, 3)
	input := createTestInputData()
	input.BomLeadTime[101] = 5

	neededDate := time.Now().AddDate(0, 0, 20) // 20 hari ke depan
	sched := scheduler.CalculateProductionSchedule(neededDate, 101, input)

	if sched.IsOverdue {
		t.Errorf("expected isOverdue false, got true")
	}

	expectedFinish := utils.BeginningOfDay(neededDate)
	expectedStart := utils.SubtractDays(expectedFinish, 5)

	if !sched.PlannedFinish.Equal(expectedFinish) {
		t.Errorf("expected finish %v, got %v", expectedFinish, sched.PlannedFinish)
	}
	if !sched.PlannedStart.Equal(expectedStart) {
		t.Errorf("expected start %v, got %v", expectedStart, sched.PlannedStart)
	}
}

func TestSchedule_Overdue(t *testing.T) {
	scheduler := domain.NewScheduler(7, 3)
	input := createTestInputData()

	// Needed date 2 hari lagi tapi lead time pembelian butuh 7 hari -> start harusnya 5 hari lalu (overdue)
	neededDate := time.Now().AddDate(0, 0, 2)
	sched := scheduler.CalculatePurchaseSchedule(neededDate, 999, input)

	if !sched.IsOverdue {
		t.Errorf("expected isOverdue true for tight lead time, got false")
	}

	expectedStart := utils.BeginningOfDay(time.Now())
	if !sched.PlannedStart.Equal(expectedStart) {
		t.Errorf("expected overdue plannedStart to be today (%v), got %v", expectedStart, sched.PlannedStart)
	}
}

func TestSchedule_DefaultLeadTime(t *testing.T) {
	scheduler := domain.NewScheduler(14, 5)
	input := createTestInputData() // kosong, tidak ada info khusus

	neededDate := time.Now().AddDate(0, 0, 30)
	schedPurchase := scheduler.CalculatePurchaseSchedule(neededDate, 555, input)

	expectedFinish := utils.BeginningOfDay(neededDate)
	expectedStart := utils.SubtractDays(expectedFinish, 14)

	if !schedPurchase.PlannedStart.Equal(expectedStart) {
		t.Errorf("expected purchase start %v with default 14 days, got %v", expectedStart, schedPurchase.PlannedStart)
	}
}
