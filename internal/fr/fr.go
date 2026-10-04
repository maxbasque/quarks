// Package fr formats dates the way the dashboard shows them, in Québec
// French ("sam. 10 oct., 9 h 05"). The standard library only knows English
// day and month names.
package fr

import (
	"fmt"
	"time"
)

var days = [...]string{"dim.", "lun.", "mar.", "mer.", "jeu.", "ven.", "sam."}

var months = [...]string{"janv.", "févr.", "mars", "avr.", "mai", "juin",
	"juil.", "août", "sept.", "oct.", "nov.", "déc."}

// Day is the abbreviated weekday: "lun.".
func Day(t time.Time) string { return days[t.Weekday()] }

// Date is the weekday and date: "sam. 10 oct.".
func Date(t time.Time) string {
	return fmt.Sprintf("%s %d %s", Day(t), t.Day(), months[t.Month()-1])
}

// DateTime adds the 24-hour time: "sam. 10 oct., 9 h 05".
func DateTime(t time.Time) string {
	return fmt.Sprintf("%s, %s", Date(t), Clock(t))
}

// Clock is the 24-hour time: "9 h 05".
func Clock(t time.Time) string {
	return fmt.Sprintf("%d h %02d", t.Hour(), t.Minute())
}

// DayMonthYear is "13 oct. 2026".
func DayMonthYear(t time.Time) string {
	return fmt.Sprintf("%d %s %d", t.Day(), months[t.Month()-1], t.Year())
}

// MonthYear is "oct. 2026".
func MonthYear(t time.Time) string {
	return fmt.Sprintf("%s %d", months[t.Month()-1], t.Year())
}
