package commands

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

// Contact is a team member /remind can message. Name is matched against
// ClickUp members the same way /<name> is.
type Contact struct {
	Name   string
	Number string // digits only, with country code
}

// remindMax caps the tasks listed, keeping the tap-to-send link short.
const remindMax = 10

// windowMargin keeps clear of the 24 hour limit, so a message sent just
// before the window closes isn't rejected.
const windowMargin = 10 * time.Minute

// remind sends one person their open tasks due today. It never pays for a
// message: if they messaged the business number in the last 24 hours the bot
// sends it directly, otherwise the owner gets a tap-to-send link instead.
func (r *Router) remind(ctx context.Context, args string) string {
	if args == "" {
		return r.remindUsage()
	}
	members, err := r.ClickUp.Members(ctx)
	if err != nil {
		return r.clickupFailed(err)
	}
	found := MatchMembers(members, args)
	switch len(found) {
	case 0:
		return fmt.Sprintf("I don't recognise *%s*. Send */team* to see names.", args)
	case 1:
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "*%s* matches %d people. Which one?\n", args, len(found))
		for _, m := range found {
			fmt.Fprintf(&b, "\n*/remind %s*  %s", strings.TrimPrefix(ShortCommand(members, m), "/"), m.DisplayName())
		}
		return b.String()
	}
	m := found[0]
	contact, ok := r.contactFor(members, m)
	if !ok {
		return fmt.Sprintf("No WhatsApp number saved for %s. Add it to TEAM_WA_NUMBERS on the server, e.g. %s=447700900123.",
			m.DisplayName(), strings.TrimPrefix(ShortCommand(members, m), "/"))
	}

	now := r.now()
	today := dateOf(now, r.Loc)
	tasks, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{
		Assignees: []int{m.ID},
		DueAfter:  today.Add(-time.Millisecond),
		DueBefore: today.AddDate(0, 0, 1),
	})
	if err != nil {
		return r.clickupFailed(err)
	}
	var due []clickup.Task
	for _, t := range tasks {
		if !t.IsDone() && t.DueDate != nil && dateOf(*t.DueDate, r.Loc).Equal(today) {
			due = append(due, t)
		}
	}
	name := firstName(m)
	if len(due) == 0 {
		return fmt.Sprintf("%s has nothing due today, so no reminder was sent. 🎉", name)
	}
	work, uploads := r.splitUploads(due)
	text := RemindMessage(name, work, uploads, now)
	count := fmt.Sprintf("%d %s due today", len(due), plural(len(due), "task", "tasks"))

	if !r.windowOpen(contact.Number) {
		return fmt.Sprintf("%s hasn't messaged the business number in the last 24 hours, so the bot can't message them for free.\n\n"+
			"Tap to send it yourself (%s):\n%s\n\n"+
			"_Send it from the business WhatsApp app. Then their reply reaches the bot and the next /remind goes automatically._",
			name, count, waLink(contact.Number, text))
	}
	if err := r.Messenger.SendText(ctx, contact.Number, text); err != nil {
		r.Log.Error("remind: send failed", "to_suffix", suffix(contact.Number), "err", err)
		var apiErr *whatsapp.APIError
		reason := "Sending failed"
		if errors.As(err, &apiErr) && apiErr.OutsideWindow() {
			reason = "WhatsApp says their 24 hour window has closed"
		}
		return fmt.Sprintf("%s, so nothing was sent to %s.\n\nTap to send it yourself (%s):\n%s", reason, name, count, waLink(contact.Number, text))
	}
	r.Log.Info("remind: sent", "to_suffix", suffix(contact.Number), "due_today", len(due))
	return fmt.Sprintf("✅ Sent %s a reminder: %s.", name, count)
}

// RemindMessage is the reminder a team member receives. now must be in the
// bot's time zone.
func RemindMessage(name string, work, uploads []clickup.Task, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s 👋\n*Due today · %s*\n\n", name, shortDate(now, now))
	sortByDue(work)
	sortByName(uploads)
	isPost := map[string]bool{}
	for _, t := range uploads {
		isPost[t.ID] = true
	}
	all := append(append([]clickup.Task(nil), work...), uploads...)
	items(&b, all, remindMax, func(t clickup.Task) (string, string) {
		if isPost[t.ID] {
			return "📤 " + truncate(uploadName(t), 80), "Post"
		}
		return truncate(t.Name, 80), titleCase(t.Status)
	})
	b.WriteString("\nReply *OK* to confirm you've seen this.")
	return b.String()
}

func (r *Router) remindUsage() string {
	var b strings.Builder
	b.WriteString("Send */remind <name>*, e.g. _/remind sunil_. The bot sends that person their tasks due today.")
	people := r.people()
	if len(people) == 0 {
		b.WriteString("\n\nNo team numbers are saved yet. Add them to TEAM_WA_NUMBERS on the server.")
		return b.String()
	}
	b.WriteString("\n\n*Numbers saved for:*")
	for _, c := range people {
		fmt.Fprintf(&b, "\n• %s", c.Name)
	}
	return b.String()
}

// contactFor finds m's saved number: the entry whose name selects exactly m.
func (r *Router) contactFor(members []clickup.Member, m clickup.Member) (Contact, bool) {
	for _, c := range r.people() {
		if found := MatchMembers(members, c.Name); len(found) == 1 && found[0].ID == m.ID {
			return c, true
		}
	}
	return Contact{}, false
}

// people are everyone /remind can message: named admins and the team.
func (r *Router) people() []Contact {
	var out []Contact
	for _, c := range append(append([]Contact(nil), r.Admins...), r.Team...) {
		if c.Name != "" {
			out = append(out, c)
		}
	}
	return out
}

// windowOpen reports whether number messaged the business number recently
// enough that free-form text to them is free.
func (r *Router) windowOpen(number string) bool {
	if r.Contacts == nil {
		return false
	}
	last, ok := r.Contacts.LastMessage(number)
	return ok && r.Now().Sub(last) < whatsapp.Window-windowMargin
}

// waLink opens a chat with number and text already typed in. Spaces must be
// %20: wa.me shows a + literally.
func waLink(number, text string) string {
	return "https://wa.me/" + number + "?text=" + strings.ReplaceAll(url.QueryEscape(text), "+", "%20")
}

func firstName(m clickup.Member) string {
	name := m.DisplayName()
	if first, _, ok := strings.Cut(name, " "); ok {
		return first
	}
	return name
}
