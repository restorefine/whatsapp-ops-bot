package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

// DueReport lists open work and social posts due today and tomorrow, plus a
// count of older overdue work. It is the daily reminder and the /due reply.
// now must be in the bot's time zone.
func DueReport(work, uploads []clickup.Task, now time.Time) string {
	loc := now.Location()
	today := dateOf(now, loc)
	tomorrow := today.AddDate(0, 0, 1)

	// split returns open tasks due today and tomorrow, and how many are overdue.
	split := func(ts []clickup.Task) (dueToday, dueTomorrow []clickup.Task, late int) {
		for _, t := range ts {
			if t.IsDone() || t.DueDate == nil {
				continue
			}
			switch d := dateOf(*t.DueDate, loc); {
			case d.Equal(today):
				dueToday = append(dueToday, t)
			case d.Equal(tomorrow):
				dueTomorrow = append(dueTomorrow, t)
			case d.Before(today):
				late++
			}
		}
		return dueToday, dueTomorrow, late
	}
	workToday, workTomorrow, overdue := split(work)
	postsToday, postsTomorrow, _ := split(uploads)

	var b strings.Builder
	header(&b, "⏰ Due soon", now)
	if len(workToday)+len(workTomorrow)+len(postsToday)+len(postsTomorrow) == 0 {
		b.WriteString("\nNothing due today or tomorrow. 🎉\n")
	}

	workSection := func(title string, ts []clickup.Task) {
		if len(ts) == 0 {
			return
		}
		sortByDue(ts)
		heading(&b, title, len(ts))
		items(&b, ts, len(ts), func(t clickup.Task) (string, string) {
			return t.Name, who(t) + " · " + titleCase(t.Status)
		})
	}
	postSection := func(title string, ts []clickup.Task) {
		if len(ts) == 0 {
			return
		}
		sortByName(ts)
		heading(&b, title, len(ts))
		for _, t := range ts {
			line := "⏳ " + uploadName(t)
			if len(t.Assignees) > 0 {
				line += " · " + who(t)
			}
			b.WriteString(line + "\n")
		}
	}
	workSection("📌 Due today", workToday)
	postSection("📤 Posting today", postsToday)
	workSection("🔜 Due tomorrow · "+shortDate(tomorrow, now), workTomorrow)
	postSection("📤 Posting tomorrow", postsTomorrow)

	if overdue > 0 {
		fmt.Fprintf(&b, "\n⚠️ _%d older %s still overdue. Send /update for details._\n", overdue, plural(overdue, "task is", "tasks are"))
	}
	return strings.TrimSpace(b.String())
}

// dueCounts is how many open items (work and posts) are due today and tomorrow.
func dueCounts(tasks []clickup.Task, now time.Time) (today, tomorrow int) {
	day := dateOf(now, now.Location())
	for _, t := range tasks {
		if t.IsDone() || t.DueDate == nil {
			continue
		}
		switch dateOf(*t.DueDate, now.Location()) {
		case day:
			today++
		case day.AddDate(0, 0, 1):
			tomorrow++
		}
	}
	return today, tomorrow
}

// dueTasks fetches open tasks due today or tomorrow, and older overdue ones.
func (r *Router) dueTasks(ctx context.Context) ([]clickup.Task, error) {
	today := dateOf(r.now(), r.Loc)
	soon, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{
		DueAfter:  today.Add(-time.Millisecond),
		DueBefore: today.AddDate(0, 0, 2),
	})
	if err != nil {
		return nil, err
	}
	overdue, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{DueBefore: today})
	if err != nil {
		return nil, err
	}
	return merge(soon, overdue), nil
}

func (r *Router) due(ctx context.Context) string {
	tasks, err := r.dueTasks(ctx)
	if err != nil {
		return r.clickupFailed(err)
	}
	work, uploads := r.splitUploads(tasks)
	return DueReport(work, uploads, r.now())
}

// Reminder sends the due-soon report to every owner once a day.
type Reminder struct {
	Router   *Router
	Owners   []string
	Hour     int
	Minute   int
	Template string // approved template used when the 24 hour window is closed; empty to skip
	Lang     string // template language code, e.g. en
}

// Run sends the reminder every day at Hour:Minute until ctx is cancelled.
func (m *Reminder) Run(ctx context.Context) {
	log := m.Router.Log
	for {
		next := NextRun(m.Router.now(), m.Hour, m.Minute)
		log.Info("reminder scheduled", "at", next.Format(time.RFC3339))
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		sendCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		m.Send(sendCtx)
		cancel()
	}
}

// Send builds the report once and delivers it to every owner. When an owner's
// 24 hour window is closed, it falls back to the template, which carries the
// counts and asks them to reply /due for the full list.
func (m *Reminder) Send(ctx context.Context) {
	r := m.Router
	tasks, err := r.dueTasks(ctx)
	if err != nil {
		r.Log.Error("reminder: clickup request failed", "err", err)
		return
	}
	now := r.now()
	work, uploads := r.splitUploads(tasks)
	report := DueReport(work, uploads, now)
	today, tomorrow := dueCounts(tasks, now)

	for _, to := range m.Owners {
		err := r.Messenger.SendText(ctx, to, report)
		var apiErr *whatsapp.APIError
		if errors.As(err, &apiErr) && apiErr.OutsideWindow() {
			if m.Template == "" {
				r.Log.Warn("reminder: 24 hour window closed and REMINDER_TEMPLATE is not set; skipped", "to_suffix", suffix(to))
				continue
			}
			err = r.Messenger.SendTemplate(ctx, to, m.Template, m.Lang, []string{strconv.Itoa(today), strconv.Itoa(tomorrow)})
		}
		if err != nil {
			r.Log.Error("reminder: send failed", "to_suffix", suffix(to), "err", err)
			continue
		}
		r.Log.Info("reminder sent", "to_suffix", suffix(to), "due_today", today, "due_tomorrow", tomorrow)
	}
}

// NextRun is the next hour:minute strictly after now, in now's time zone.
func NextRun(now time.Time, hour, minute int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, now.Location())
	}
	return next
}

// suffix keeps phone numbers out of logs apart from the last three digits.
func suffix(n string) string {
	if len(n) <= 3 {
		return n
	}
	return "…" + n[len(n)-3:]
}
