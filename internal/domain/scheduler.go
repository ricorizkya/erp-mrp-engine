package domain

import (
	"time"

	"erp-mrp-engine/internal/utils"
)

// Schedule menyimpan hasil perhitungan tanggal mulai dan selesai dari suatu order.
type Schedule struct {
	PlannedStart  time.Time `json:"plannedStart"`
	PlannedFinish time.Time `json:"plannedFinish"`
	IsOverdue     bool      `json:"isOverdue"`
}

// Scheduler melakukan backward scheduling berdasarkan needed_date dan lead time.
type Scheduler struct {
	defaultLeadTimeDays   int
	defaultProductionDays int
}

// NewScheduler membuat instance Scheduler baru dengan parameter konfigurasi default.
func NewScheduler(defaultLeadTimeDays, defaultProductionDays int) *Scheduler {
	if defaultLeadTimeDays <= 0 {
		defaultLeadTimeDays = 7
	}
	if defaultProductionDays <= 0 {
		defaultProductionDays = 3
	}
	return &Scheduler{
		defaultLeadTimeDays:   defaultLeadTimeDays,
		defaultProductionDays: defaultProductionDays,
	}
}

// CalculateProductionSchedule menghitung jadwal produksi mundur dari tanggal dibutuhkan.
func (s *Scheduler) CalculateProductionSchedule(
	neededDate time.Time,
	bomVersionID int64,
	input *InputData,
) Schedule {
	leadTimeDays := s.defaultProductionDays
	if input != nil {
		if lt, exists := input.BomLeadTime[bomVersionID]; exists && lt > 0 {
			leadTimeDays = lt
		}
	}

	return s.calculateSchedule(neededDate, leadTimeDays)
}

// CalculatePurchaseSchedule menghitung jadwal pembelian bahan baku mundur dari tanggal dibutuhkan.
func (s *Scheduler) CalculatePurchaseSchedule(
	neededDate time.Time,
	variantID int64,
	input *InputData,
) Schedule {
	leadTimeDays := s.defaultLeadTimeDays
	if input != nil {
		if info, exists := input.VariantInfo[variantID]; exists && info.LeadTimeDays > 0 {
			leadTimeDays = info.LeadTimeDays
		}
	}

	return s.calculateSchedule(neededDate, leadTimeDays)
}

func (s *Scheduler) calculateSchedule(neededDate time.Time, leadTimeDays int) Schedule {
	plannedFinish := utils.BeginningOfDay(neededDate)
	plannedStart := utils.SubtractDays(plannedFinish, leadTimeDays)

	isOverdue := utils.IsBeforeToday(plannedStart)
	if isOverdue {
		plannedStart = utils.BeginningOfDay(time.Now().UTC())
	}

	return Schedule{
		PlannedStart:  plannedStart,
		PlannedFinish: plannedFinish,
		IsOverdue:     isOverdue,
	}
}
