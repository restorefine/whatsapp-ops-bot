package clickup

import "time"

// Dates turns ClickUp due dates into deadlines in the bot's time zone.
//
// A due date set without a time is stored by ClickUp as 04:00 in the time
// zone of whoever set it. A date picked in Nepal as "Thu 8 Oct" is therefore
// stored as 23:15 on Wed 7 Oct in the UK. Normalise recognises 04:00 in any
// of SetIn and moves the deadline to the end of the date that was picked, in
// Loc. Due dates with a real time are left alone.
type Dates struct {
	Loc   *time.Location   // the bot's time zone; deadlines are judged here
	SetIn []*time.Location // time zones the team sets due dates in
}

// shift is how much later a normalised deadline can be than the stored time:
// 04:00 at UTC+14 to 23:59:59 at UTC-12 is under 46 hours.
const shift = 46 * time.Hour

// Normalise rewrites t.DueDate for a date-only due date and sets DueHasTime.
func (d Dates) Normalise(t *Task) {
	if t.DueDate == nil || d.Loc == nil {
		return
	}
	for _, zone := range d.SetIn {
		local := t.DueDate.In(zone)
		if local.Hour() == 4 && local.Minute() == 0 && local.Second() == 0 {
			end := time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 59, 0, d.Loc)
			t.DueDate, t.DueHasTime = &end, false
			return
		}
	}
	t.DueHasTime = true
}

// widen loosens a due date filter so ClickUp returns every task whose
// normalised deadline could fall inside it.
func (d Dates) widen(f TaskFilter) TaskFilter {
	if d.Loc != nil && !f.DueAfter.IsZero() {
		f.DueAfter = f.DueAfter.Add(-shift)
	}
	return f
}

// keep reports whether a normalised task still matches the caller's filter.
// Without a time zone nothing was widened, so ClickUp's own filtering stands.
func (d Dates) keep(t Task, f TaskFilter) bool {
	if d.Loc == nil || t.DueDate == nil {
		return true
	}
	if !f.DueAfter.IsZero() && !t.DueDate.After(f.DueAfter) {
		return false
	}
	if !f.DueBefore.IsZero() && !t.DueDate.Before(f.DueBefore) {
		return false
	}
	return true
}
