package commands

import (
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
)

// Clock labels times in the bot's time zone and, optionally, a second one the
// team works in, e.g. "9:24 AM UK · 2:09 PM Nepal".
type Clock struct {
	Label       string         // the bot's time zone, e.g. "UK"; empty shows no label
	Second      *time.Location // nil shows only the bot's time
	SecondLabel string         // e.g. "Nepal"
}

// timeLayout is how every time is shown: 12-hour with AM/PM, e.g. "9:24 AM".
const timeLayout = "3:04 PM"

// clock is set once at startup by SetClock. The zero value shows plain times.
var clock Clock

// SetClock sets how times are labelled in every message.
func SetClock(c Clock) { clock = c }

// stamp is "9:24 AM UK · 2:09 PM Nepal" for t, or "9:24 AM" with no labels set.
func stamp(t time.Time) string {
	s := t.Format(timeLayout)
	if clock.Label != "" {
		s += " " + clock.Label
	}
	if clock.Second != nil {
		s += " · " + t.In(clock.Second).Format(timeLayout) + " " + clock.SecondLabel
	}
	return s
}

// dueClock is the time part of a due date that has one: "6:00 PM UK (10:45 PM Nepal)".
// Date-only due dates have none, so it returns "".
func dueClock(t clickup.Task, now time.Time) string {
	if t.DueDate == nil || !t.DueHasTime {
		return ""
	}
	d := t.DueDate.In(now.Location())
	s := d.Format(timeLayout)
	if clock.Label != "" {
		s += " " + clock.Label
	}
	if clock.Second != nil {
		s += " (" + d.In(clock.Second).Format(timeLayout) + " " + clock.SecondLabel + ")"
	}
	return s
}
