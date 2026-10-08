package commands

import (
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
)

// Clock labels times in the bot's time zone and, optionally, a second one the
// team works in, e.g. "09:24 UK · 14:09 Nepal".
type Clock struct {
	Label       string         // the bot's time zone, e.g. "UK"; empty shows no label
	Second      *time.Location // nil shows only the bot's time
	SecondLabel string         // e.g. "Nepal"
}

// clock is set once at startup by SetClock. The zero value shows plain times.
var clock Clock

// SetClock sets how times are labelled in every message.
func SetClock(c Clock) { clock = c }

// stamp is "09:24 UK · 14:09 Nepal" for t, or "09:24" with no labels set.
func stamp(t time.Time) string {
	s := t.Format("15:04")
	if clock.Label != "" {
		s += " " + clock.Label
	}
	if clock.Second != nil {
		s += " · " + t.In(clock.Second).Format("15:04") + " " + clock.SecondLabel
	}
	return s
}

// dueClock is the time part of a due date that has one: "18:00 UK (22:45 Nepal)".
// Date-only due dates have none, so it returns "".
func dueClock(t clickup.Task, now time.Time) string {
	if t.DueDate == nil || !t.DueHasTime {
		return ""
	}
	d := t.DueDate.In(now.Location())
	s := d.Format("15:04")
	if clock.Label != "" {
		s += " " + clock.Label
	}
	if clock.Second != nil {
		s += " (" + d.In(clock.Second).Format("15:04") + " " + clock.SecondLabel + ")"
	}
	return s
}
