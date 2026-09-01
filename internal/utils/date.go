// Package utils menyediakan fungsi utilitas tanggal dan waktu untuk penjadwalan MRP.
package utils

import (
	"time"
)

// SubtractDays mengurangi waktu t sebanyak days hari (24 * days jam).
func SubtractDays(t time.Time, days int) time.Time {
	return t.AddDate(0, 0, -days)
}

// AddDays menambah waktu t sebanyak days hari.
func AddDays(t time.Time, days int) time.Time {
	return t.AddDate(0, 0, days)
}

// BeginningOfDay mengembalikan waktu t di awal hari (00:00:00.000 UTC).
func BeginningOfDay(t time.Time) time.Time {
	utc := t.UTC()
	y, m, d := utc.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// IsBeforeToday memeriksa apakah tanggal t berada sebelum hari ini (membandingkan tanggalnya saja dalam UTC).
func IsBeforeToday(t time.Time) bool {
	today := BeginningOfDay(time.Now().UTC())
	target := BeginningOfDay(t.UTC())
	return target.Before(today)
}

// MaxTime mengembalikan waktu yang lebih baru antara a dan b.
func MaxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// MinTime mengembalikan waktu yang lebih lama/awal antara a dan b.
func MinTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
