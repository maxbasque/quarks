package fr

import (
	"testing"
	"time"
)

func TestFormats(t *testing.T) {
	d := time.Date(2026, 10, 10, 9, 5, 0, 0, time.UTC)
	for got, want := range map[string]string{
		Day(d):          "sam.",
		Date(d):         "sam. 10 oct.",
		DateTime(d):     "sam. 10 oct., 9 h 05",
		DayMonthYear(d): "10 oct. 2026",
		MonthYear(d):    "oct. 2026",
		Clock(time.Date(2026, 8, 2, 21, 30, 0, 0, time.UTC)): "21 h 30",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
