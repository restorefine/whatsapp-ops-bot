package commands

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
)

// Section caps keep replies readable on a phone.
const (
	maxSectionItems = 15 // member report sections
	maxOlderOverdue = 10 // "overdue from earlier months" in /update
)

// MemberReport shows one person's numbers, their overdue and open tasks grouped
// by status, and what they completed this month. now must be in the bot's time zone.
func MemberReport(m clickup.Member, tasks []clickup.Task, now time.Time) string {
	loc := now.Location()
	today := dateOf(now, loc)
	monthStart := startOfMonth(now)
	firstName := strings.ToLower(strings.Fields(m.DisplayName() + " x")[0])

	var overdue, done []clickup.Task
	byStatus := map[string][]clickup.Task{}
	statusType := map[string]string{}
	open, dueThisWeek := 0, 0
	for _, t := range tasks {
		if !t.AssignedTo(m.ID) {
			continue
		}
		if t.IsDone() {
			if c := t.CompletedAt(); c != nil && !c.Before(monthStart) {
				done = append(done, t)
			}
			continue
		}
		open++
		if t.DueDate != nil {
			days := daysBetween(today, dateOf(*t.DueDate, loc))
			if days < 0 {
				overdue = append(overdue, t)
				continue
			}
			if days < 7 {
				dueThisWeek++
			}
		}
		key := strings.ToLower(t.Status)
		byStatus[key] = append(byStatus[key], t)
		statusType[key] = t.StatusType
	}

	var b strings.Builder
	header(&b, "👤 "+m.DisplayName(), now)
	if open == 0 && len(done) == 0 {
		b.WriteString("\nNo open tasks, and nothing completed this month.")
		return b.String()
	}
	b.WriteString("\n")
	stats(&b, [][2]string{
		{"Open tasks", fmt.Sprint(open)},
		{"Overdue", fmt.Sprint(len(overdue))},
		{"Due in next 7 days", fmt.Sprint(dueThisWeek)},
		{"Completed in " + now.Format("Jan"), fmt.Sprint(len(done))},
	})

	detail := func(t clickup.Task, parts ...string) string {
		if t.ListName != "" && strings.ToLower(t.ListName) != firstName {
			parts = append(parts, t.ListName)
		}
		return strings.Join(parts, " · ")
	}

	if len(overdue) > 0 {
		sortByDue(overdue)
		heading(&b, "⚠️ Overdue", len(overdue))
		items(&b, overdue, maxSectionItems, func(t clickup.Task) (string, string) {
			return t.Name, detail(t, dueText(t, now), titleCase(t.Status))
		})
	}

	statuses := make([]string, 0, len(byStatus))
	for s := range byStatus {
		statuses = append(statuses, s)
	}
	// Work in progress (custom statuses) first, then "to do" style open statuses.
	sort.Slice(statuses, func(i, j int) bool {
		oi, oj := statusType[statuses[i]] == "open", statusType[statuses[j]] == "open"
		if oi != oj {
			return !oi
		}
		return statuses[i] < statuses[j]
	})
	for _, s := range statuses {
		ts := byStatus[s]
		sortByDue(ts)
		heading(&b, "🔹 "+titleCase(s), len(ts))
		items(&b, ts, maxSectionItems, func(t clickup.Task) (string, string) {
			return t.Name, detail(t, dueText(t, now))
		})
	}

	if len(done) > 0 {
		sort.Slice(done, func(i, j int) bool { return done[i].CompletedAt().After(*done[j].CompletedAt()) })
		heading(&b, "✅ Completed in "+now.Format("January"), len(done))
		items(&b, done, maxSectionItems, func(t clickup.Task) (string, string) {
			return t.Name, detail(t, "Completed "+shortDate(*t.CompletedAt(), now))
		})
	}
	return strings.TrimSpace(b.String())
}

// MonthReport is the founder's overview: headline numbers, a per-person table,
// older overdue work, a day-by-day calendar of this month, a summary of the
// posting calendar and a preview of next month. now must be in the bot's time zone.
func MonthReport(tasks, uploads []clickup.Task, now time.Time) string {
	loc := now.Location()
	today := dateOf(now, loc)
	monthStart := startOfMonth(now)
	nextStart := monthStart.AddDate(0, 1, 0)
	afterNext := monthStart.AddDate(0, 2, 0)

	type row struct{ open, late, done int }
	people := map[string]*row{}
	person := func(name string) *row {
		if people[name] == nil {
			people[name] = &row{}
		}
		return people[name]
	}

	var earlier, thisMonth, nextMonth []clickup.Task
	var doneDue, openDue, overdueDue int
	for _, t := range tasks {
		names := t.AssigneeNames()
		if len(names) == 0 {
			names = []string{"Unassigned"}
		}
		late := !t.IsDone() && t.DueDate != nil && dateOf(*t.DueDate, loc).Before(today)
		for _, n := range names {
			switch {
			case t.IsDone():
				if c := t.CompletedAt(); c != nil && !c.Before(monthStart) {
					person(n).done++
				}
			default:
				person(n).open++
				if late {
					person(n).late++
				}
			}
		}

		if t.DueDate == nil {
			continue
		}
		d := dateOf(*t.DueDate, loc)
		switch {
		case d.Before(monthStart):
			if !t.IsDone() {
				earlier = append(earlier, t)
			}
		case d.Before(nextStart):
			thisMonth = append(thisMonth, t)
			switch {
			case t.IsDone():
				doneDue++
			case late:
				overdueDue++
			default:
				openDue++
			}
		case d.Before(afterNext):
			nextMonth = append(nextMonth, t)
		}
	}

	var b strings.Builder
	header(&b, "📊 Monthly Update · "+now.Format("January 2006"), now)

	b.WriteString("\n*Overview*\n")
	stats(&b, [][2]string{
		{"Due this month", fmt.Sprint(len(thisMonth))},
		{"Completed", fmt.Sprint(doneDue)},
		{"Still open", fmt.Sprint(openDue)},
		{"Overdue", fmt.Sprint(overdueDue)},
		{"Overdue, older", fmt.Sprint(len(earlier))},
	})

	if len(people) > 0 {
		names := make([]string, 0, len(people))
		for n := range people {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool {
			a, c := names[i], names[j]
			if (a == "Unassigned") != (c == "Unassigned") {
				return c == "Unassigned"
			}
			if people[a].open != people[c].open {
				return people[a].open > people[c].open
			}
			return a < c
		})
		rows := [][]string{{"Name", "Open", "Late", "Done"}}
		for _, n := range names {
			p := people[n]
			rows = append(rows, []string{n, fmt.Sprint(p.open), fmt.Sprint(p.late), fmt.Sprint(p.done)})
		}
		b.WriteString("\n*Team*\n")
		table(&b, rows)
		b.WriteString("_Late = overdue · Done = completed in " + now.Format("January") + "_\n")
	}

	if len(earlier) > 0 {
		sortByDue(earlier)
		heading(&b, "⚠️ Overdue from earlier months", len(earlier))
		items(&b, earlier, maxOlderOverdue, func(t clickup.Task) (string, string) {
			return t.Name, strings.Join([]string{who(t), lateText(t, now), titleCase(t.Status)}, " · ")
		})
		if len(earlier) > maxOlderOverdue {
			b.WriteString("_Send a name, e.g. /sunil, to see everything for one person._\n")
		}
	}

	b.WriteString("\n*📅 " + now.Format("January") + " calendar*\n")
	if len(thisMonth) == 0 {
		b.WriteString("Nothing due this month.\n")
	} else {
		b.WriteString("_✅ done · ⏳ open · ⚠️ overdue_\n")
		calendar(&b, thisMonth, now)
	}

	uploadsSummary(&b, uploads, now)

	b.WriteString("\n*🔭 Coming up · " + nextStart.Format("January 2006") + "*\n")
	if len(nextMonth) == 0 {
		b.WriteString("Nothing scheduled yet.")
	} else {
		fmt.Fprintf(&b, "%d %s due\n", len(nextMonth), plural(len(nextMonth), "task", "tasks"))
		calendar(&b, nextMonth, now)
	}
	return strings.TrimSpace(b.String())
}

// UploadsReport is the posting calendar for the month starting at month, one
// post per line. For the current month it also lists earlier posts that were
// never marked as posted.
func UploadsReport(folder string, uploads []clickup.Task, month, now time.Time) string {
	loc := now.Location()
	today := dateOf(now, loc)
	monthEnd := month.AddDate(0, 1, 0)

	var inMonth, missedEarlier []clickup.Task
	for _, t := range uploads {
		if t.DueDate == nil {
			continue
		}
		d := dateOf(*t.DueDate, loc)
		switch {
		case !d.Before(month) && d.Before(monthEnd):
			inMonth = append(inMonth, t)
		case d.Before(month) && !t.IsDone():
			missedEarlier = append(missedEarlier, t)
		}
	}

	var b strings.Builder
	header(&b, "📤 "+titleCase(folder)+" · "+month.Format("January 2006"), now)
	if len(inMonth) == 0 {
		b.WriteString("\nNothing scheduled.\n")
	} else {
		posted, missed, toGo := countUploads(inMonth, today)
		b.WriteString("\n")
		stats(&b, [][2]string{
			{"Scheduled", fmt.Sprint(len(inMonth))},
			{"Posted", fmt.Sprint(posted)},
			{"Missed", fmt.Sprint(missed)},
			{"Upcoming", fmt.Sprint(toGo)},
		})
	}

	if len(missedEarlier) > 0 {
		sortByDue(missedEarlier)
		heading(&b, "⚠️ Not marked as posted, from earlier", len(missedEarlier))
		items(&b, missedEarlier, maxSectionItems, func(t clickup.Task) (string, string) {
			return uploadName(t), dueText(t, now)
		})
	}

	if len(inMonth) > 0 {
		b.WriteString("\n*📅 Posting calendar*\n_✅ posted · ⏳ upcoming · ⚠️ missed_\n")
		sortByDue(inMonth)
		var current time.Time
		for _, t := range inMonth {
			if d := dateOf(*t.DueDate, loc); !d.Equal(current) {
				current = d
				b.WriteString("\n" + dayLabel(d, now) + "\n")
			}
			b.WriteString(uploadMarker(t, today) + " " + uploadName(t) + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// uploadsSummary writes the posting calendar section of /update.
func uploadsSummary(b *strings.Builder, uploads []clickup.Task, now time.Time) {
	today := dateOf(now, now.Location())
	monthStart := startOfMonth(now)
	var inMonth, todays []clickup.Task
	for _, t := range uploads {
		if t.DueDate == nil {
			continue
		}
		d := dateOf(*t.DueDate, now.Location())
		if d.Before(monthStart) || !d.Before(monthStart.AddDate(0, 1, 0)) {
			continue
		}
		inMonth = append(inMonth, t)
		if d.Equal(today) {
			todays = append(todays, t)
		}
	}
	if len(inMonth) == 0 {
		return
	}
	posted, missed, toGo := countUploads(inMonth, today)
	b.WriteString("\n*📤 Social uploads*\n")
	stats(b, [][2]string{
		{"Scheduled", fmt.Sprint(len(inMonth))},
		{"Posted", fmt.Sprint(posted)},
		{"Missed", fmt.Sprint(missed)},
		{"Upcoming", fmt.Sprint(toGo)},
	})
	if len(todays) > 0 {
		sortByName(todays)
		parts := make([]string, len(todays))
		for i, t := range todays {
			parts[i] = uploadMarker(t, today) + " " + uploadName(t)
		}
		b.WriteString("Today: " + strings.Join(parts, ", ") + "\n")
	}
	b.WriteString("_Send /uploads for the full posting calendar._\n")
}

// calendar writes tasks grouped under a heading per due day.
func calendar(b *strings.Builder, tasks []clickup.Task, now time.Time) {
	loc := now.Location()
	today := dateOf(now, loc)
	sortByDue(tasks)
	var current time.Time
	for _, t := range tasks {
		d := dateOf(*t.DueDate, loc)
		if !d.Equal(current) {
			current = d
			b.WriteString("\n" + dayLabel(d, now) + "\n")
		}
		marker := "⏳"
		switch {
		case t.IsDone():
			marker = "✅"
		case d.Before(today):
			marker = "⚠️"
		}
		line := marker + " " + t.Name + " · " + who(t)
		if !t.IsDone() {
			line += " · _" + titleCase(t.Status) + "_"
		}
		b.WriteString(line + "\n")
	}
}

// --- layout helpers ---

// header writes a bold title and a muted timestamp line.
func header(b *strings.Builder, title string, now time.Time) {
	b.WriteString("*" + title + "*\n_" + now.Format("Mon 2 Jan 2006 · 15:04") + "_\n")
}

func heading(b *strings.Builder, title string, count int) {
	fmt.Fprintf(b, "\n*%s* (%d)\n", title, count)
}

// items writes a bulleted list with the task on one line and muted details
// below it, showing at most max items.
func items(b *strings.Builder, tasks []clickup.Task, max int, render func(clickup.Task) (title, detail string)) {
	for i, t := range tasks {
		if i == max {
			fmt.Fprintf(b, "_+%d more_\n", len(tasks)-max)
			return
		}
		title, detail := render(t)
		b.WriteString("• " + title + "\n")
		if detail != "" {
			b.WriteString("   _" + detail + "_\n")
		}
	}
}

// stats writes label/value pairs as an aligned monospace block.
func stats(b *strings.Builder, rows [][2]string) {
	width := 0
	for _, r := range rows {
		width = max(width, runeLen(r[0])+runeLen(r[1]))
	}
	width += 3
	b.WriteString("```\n")
	for _, r := range rows {
		b.WriteString(r[0] + strings.Repeat(" ", width-runeLen(r[0])-runeLen(r[1])) + r[1] + "\n")
	}
	b.WriteString("```\n")
}

// table writes rows as a monospace table: the first column left-aligned and
// cut to 10 characters, the others right-aligned. It stays narrow enough not
// to wrap on a phone.
func table(b *strings.Builder, rows [][]string) {
	const nameWidth = 10
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			if i == 0 {
				c = truncate(c, nameWidth)
			}
			widths[i] = max(widths[i], runeLen(c))
		}
	}
	b.WriteString("```\n")
	for _, r := range rows {
		var line strings.Builder
		for i, c := range r {
			if i == 0 {
				c = truncate(c, nameWidth)
				line.WriteString(c + strings.Repeat(" ", widths[0]-runeLen(c)))
				continue
			}
			line.WriteString(strings.Repeat(" ", widths[i]-runeLen(c)+2) + c)
		}
		b.WriteString(line.String() + "\n")
	}
	b.WriteString("```\n")
}

func dayLabel(d, now time.Time) string {
	label := "*" + shortDate(d, now) + "*"
	if d.Equal(dateOf(now, now.Location())) {
		label += " · _today_"
	}
	return label
}

// --- text helpers ---

func who(t clickup.Task) string {
	names := t.AssigneeNames()
	if len(names) == 0 {
		return "Unassigned"
	}
	return strings.Join(names, ", ")
}

// dueText describes a due date relative to now: "Due today",
// "Due Tue 6 Oct" or "Due Mon 28 Sep · 5 days late".
func dueText(t clickup.Task, now time.Time) string {
	if t.DueDate == nil {
		return "No due date"
	}
	days := daysBetween(dateOf(now, now.Location()), dateOf(*t.DueDate, now.Location()))
	switch {
	case days == 0:
		return "Due today"
	case days == 1:
		return "Due tomorrow"
	case days < 0:
		return "Due " + shortDate(*t.DueDate, now) + " · " + lateText(t, now)
	default:
		return "Due " + shortDate(*t.DueDate, now)
	}
}

// lateText is "5 days late", or the due date when the task is not late.
func lateText(t clickup.Task, now time.Time) string {
	if t.DueDate == nil {
		return "No due date"
	}
	days := daysBetween(dateOf(*t.DueDate, now.Location()), dateOf(now, now.Location()))
	if days <= 0 {
		return "Due " + shortDate(*t.DueDate, now)
	}
	return fmt.Sprintf("%d %s late", days, plural(days, "day", "days"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		words[i] = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	return strings.Join(words, " ")
}

func runeLen(s string) int { return len([]rune(s)) }

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "."
}

// --- uploads ---

func countUploads(ts []clickup.Task, today time.Time) (posted, missed, toGo int) {
	for _, t := range ts {
		switch uploadMarker(t, today) {
		case "✅":
			posted++
		case "⚠️":
			missed++
		default:
			toGo++
		}
	}
	return posted, missed, toGo
}

func uploadMarker(t clickup.Task, today time.Time) string {
	switch {
	case t.IsDone():
		return "✅"
	case t.DueDate != nil && dateOf(*t.DueDate, today.Location()).Before(today):
		return "⚠️"
	default:
		return "⏳"
	}
}

// uploadName is the task name, prefixed with its list (the client) when the
// name does not already mention it. Mentioning the list's first word counts,
// ignoring spaces, so "Zaks(G)" in list "Zaks Drive thru" and "Fav Spot (V)"
// in list "FavSpot" are left alone.
func uploadName(t clickup.Task) string {
	words := strings.Fields(strings.ToLower(t.ListName))
	name := strings.ReplaceAll(strings.ToLower(t.Name), " ", "")
	if len(words) == 0 || strings.Contains(name, words[0]) {
		return t.Name
	}
	return t.ListName + ": " + t.Name
}

func sortByName(ts []clickup.Task) {
	sort.SliceStable(ts, func(i, j int) bool { return strings.ToLower(uploadName(ts[i])) < strings.ToLower(uploadName(ts[j])) })
}

// --- dates ---

// sortByDue orders by due date (missing dates last), then name.
func sortByDue(ts []clickup.Task) {
	sort.SliceStable(ts, func(i, j int) bool {
		a, b := ts[i].DueDate, ts[j].DueDate
		switch {
		case a == nil && b == nil:
			return strings.ToLower(ts[i].Name) < strings.ToLower(ts[j].Name)
		case a == nil:
			return false
		case b == nil:
			return true
		case !a.Equal(*b):
			return a.Before(*b)
		default:
			return strings.ToLower(ts[i].Name) < strings.ToLower(ts[j].Name)
		}
	})
}

// dateOf is midnight of t's calendar day in loc.
func dateOf(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

func startOfMonth(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
}

// daysBetween counts calendar days from a to b, ignoring DST shifts.
func daysBetween(a, b time.Time) int {
	day := func(t time.Time) int64 {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400
	}
	return int(day(b) - day(a))
}

// shortDate is "Tue 6 Oct", with the year added when it is not this year.
func shortDate(t, now time.Time) string {
	t = t.In(now.Location())
	if t.Year() != now.Year() {
		return t.Format("Mon 2 Jan 2006")
	}
	return t.Format("Mon 2 Jan")
}
