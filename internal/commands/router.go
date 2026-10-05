package commands

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

// HelpText lists every command.
const HelpText = `*🤖 Ops Bot · Commands*

*/update*
Monthly overview: team table, calendar, overdue work and uploads

*/due*
Everything due today and tomorrow, work and posts

*/uploads*
Social media posting calendar. _/uploads next_ shows next month

*/<name>*
One person's tasks, e.g. _/sunil_

*/team*
Team members and their commands

*/help*
This list`

// NonTextReply answers images, voice notes and other non-text messages.
const NonTextReply = "I only understand text commands. Send /help to see them."

var reserved = map[string]bool{
	"help": true, "start": true, "menu": true,
	"update": true, "updates": true,
	"team": true, "members": true,
	"uploads": true, "upload": true, "posts": true,
	"due": true, "today": true, "reminder": true,
}

// Router turns inbound messages into replies. It depends only on interfaces.
type Router struct {
	ClickUp   clickup.Client
	Messenger whatsapp.Messenger
	Uploads   string // folder name of the posting calendar; empty disables /uploads
	Loc       *time.Location
	Now       func() time.Time
	Log       *slog.Logger
}

// Process answers one inbound message. It implements whatsapp.Processor.
func (r *Router) Process(ctx context.Context, msg whatsapp.InboundMessage) {
	start := time.Now()
	reply := NonTextReply
	cmd := "(non-text)"
	if msg.Type == "text" {
		cmd = Parse(msg.Text).Name
		reply = r.Reply(ctx, msg.Text)
	}
	if err := r.Messenger.SendText(ctx, msg.From, reply); err != nil {
		r.Log.Error("failed to send reply", "command", cmd, "wamid", msg.ID, "err", err)
		return
	}
	r.Log.Info("command handled", "command", cmd, "wamid", msg.ID, "reply_chars", len([]rune(reply)), "took_ms", time.Since(start).Milliseconds())
}

// Reply returns the response text for a message.
func (r *Router) Reply(ctx context.Context, text string) string {
	cmd := Parse(text)
	switch cmd.Name {
	case "", "help", "start", "menu":
		return HelpText
	case "update", "updates":
		return r.update(ctx)
	case "due", "today", "reminder":
		return r.due(ctx)
	case "team", "members":
		return r.team(ctx)
	case "uploads", "upload", "posts":
		return r.uploads(ctx, strings.EqualFold(cmd.Args, "next"))
	default:
		return r.member(ctx, cmd)
	}
}

func (r *Router) now() time.Time { return r.Now().In(r.Loc) }

func (r *Router) clickupFailed(err error) string {
	r.Log.Error("clickup request failed", "err", err)
	return "Couldn't fetch data from ClickUp right now. Try again in a minute.\n_" + err.Error() + "_"
}

func (r *Router) team(ctx context.Context) string {
	members, err := r.ClickUp.Members(ctx)
	if err != nil {
		return r.clickupFailed(err)
	}
	if len(members) == 0 {
		return "No members found in this ClickUp workspace."
	}
	sorted := append([]clickup.Member(nil), members...)
	sortMembers(sorted)
	var b strings.Builder
	fmt.Fprintf(&b, "*👥 Team* (%d)\n_Send a command to see that person's tasks_\n", len(sorted))
	for _, m := range sorted {
		fmt.Fprintf(&b, "\n*%s*  %s", ShortCommand(members, m), m.DisplayName())
	}
	return b.String()
}

func (r *Router) member(ctx context.Context, cmd Command) string {
	members, err := r.ClickUp.Members(ctx)
	if err != nil {
		return r.clickupFailed(err)
	}
	found := MatchMembers(members, cmd.Full())
	switch len(found) {
	case 0:
		return fmt.Sprintf("I don't recognise */%s*. Send */team* to see names.\n\n%s", cmd.Full(), HelpText)
	case 1:
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "*/%s* matches %d people. Which one?\n", cmd.Full(), len(found))
		for _, m := range found {
			fmt.Fprintf(&b, "\n*%s*  %s", ShortCommand(members, m), m.DisplayName())
		}
		return b.String()
	}

	m := found[0]
	now := r.now()
	monthStart := startOfMonth(now)

	open, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{Assignees: []int{m.ID}})
	if err != nil {
		return r.clickupFailed(err)
	}
	recent, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{Assignees: []int{m.ID}, IncludeClosed: true, UpdatedAfter: monthStart})
	if err != nil {
		return r.clickupFailed(err)
	}
	return MemberReport(m, merge(open, recent), now)
}

func (r *Router) update(ctx context.Context) string {
	now := r.now()
	monthStart := startOfMonth(now)
	afterNext := monthStart.AddDate(0, 2, 0)

	due, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{
		DueAfter:      monthStart.Add(-time.Millisecond),
		DueBefore:     afterNext,
		IncludeClosed: true,
	})
	if err != nil {
		return r.clickupFailed(err)
	}
	completed, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{UpdatedAfter: monthStart, IncludeClosed: true})
	if err != nil {
		return r.clickupFailed(err)
	}
	// Every open task, for the team table and older overdue work.
	open, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{})
	if err != nil {
		return r.clickupFailed(err)
	}
	work, uploads := r.splitUploads(merge(due, completed, open))
	return MonthReport(work, uploads, now)
}

func (r *Router) uploads(ctx context.Context, next bool) string {
	if r.Uploads == "" {
		return "/uploads is turned off. Set UPLOADS_FOLDER to the ClickUp folder that holds your posting calendar."
	}
	now := r.now()
	monthStart := startOfMonth(now)
	month := monthStart
	if next {
		month = monthStart.AddDate(0, 1, 0)
	}
	due, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{
		DueAfter:      month.Add(-time.Millisecond),
		DueBefore:     month.AddDate(0, 1, 0),
		IncludeClosed: true,
	})
	if err != nil {
		return r.clickupFailed(err)
	}
	var earlier []clickup.Task
	if !next {
		// Posts from the last 30 days that were never marked as posted.
		if earlier, err = r.ClickUp.Tasks(ctx, clickup.TaskFilter{DueAfter: monthStart.AddDate(0, 0, -30), DueBefore: monthStart}); err != nil {
			return r.clickupFailed(err)
		}
	}
	_, uploads := r.splitUploads(merge(due, earlier))
	return UploadsReport(r.Uploads, uploads, month, now)
}

// splitUploads separates posting-calendar tasks from team work.
func (r *Router) splitUploads(tasks []clickup.Task) (work, uploads []clickup.Task) {
	for _, t := range tasks {
		if r.Uploads != "" && strings.EqualFold(t.FolderName, r.Uploads) {
			uploads = append(uploads, t)
		} else {
			work = append(work, t)
		}
	}
	return work, uploads
}

// merge combines task lists, dropping duplicates by ID.
func merge(lists ...[]clickup.Task) []clickup.Task {
	seen := map[string]bool{}
	var out []clickup.Task
	for _, list := range lists {
		for _, t := range list {
			if !seen[t.ID] {
				seen[t.ID] = true
				out = append(out, t)
			}
		}
	}
	return out
}
