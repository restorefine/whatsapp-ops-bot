package commands

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
)

// Role is what a phone number may do.
type Role int

const (
	RoleNone  Role = iota // unknown number: ignored
	RoleTeam              // own tasks only: my tasks, details, done, undo
	RoleAdmin             // founders and the project manager: everything
)

// TeamHelpText lists the commands team members can use.
const TeamHelpText = `*🤖 Ops Bot · Your commands*

*my tasks*
Your open tasks, numbered

*<number>*
The brief and links for that task, e.g. _3_

*done <task>*
Mark a task complete, e.g. _done weetutor_ or _done 2, 3_

*undo*
Reopen what you just completed (within 10 minutes)`

// AdminOnlyReply answers a team member who sends an admin command.
const AdminOnlyReply = "That command is for the founders and project manager. Send *help* to see yours."

const (
	maxListed   = 30   // tasks shown by my tasks
	maxBrief    = 1500 // characters of a description
	maxComments = 3
)

// whatsappMarker ends the comments the bot leaves, so they can be left out
// of task details.
const whatsappMarker = "via WhatsApp"

var numbersOnly = regexp.MustCompile(`^\s*\d+(\s*[,\s]\s*\d+)*\s*$`)

// selfService handles the commands everyone can use about their own tasks.
// ok is false when text is not one of them.
func (r *Router) selfService(ctx context.Context, p Contact, role Role, text string) (reply string, ok bool) {
	raw := strings.TrimSpace(text)
	if numbersOnly.MatchString(raw) {
		return r.pick(ctx, p, role, parseNumbers(raw)), true
	}
	cmd := Parse(raw)
	words := strings.Fields(strings.ToLower(cmd.Full()))
	switch {
	case cmd.Name == "my" && strings.EqualFold(cmd.Args, "tasks"), cmd.Name == "mytasks":
		return r.myTasks(ctx, p, role), true
	case cmd.Name == "undo" && cmd.Args == "":
		return r.undo(ctx, p), true
	case isDoneWord(cmd.Name):
		return r.done(ctx, p, role, cmd.Args), true
	case len(words) > 1 && isDoneWord(words[len(words)-1]):
		rest := strings.Fields(cmd.Name + " " + cmd.Args)
		return r.done(ctx, p, role, strings.Join(rest[:len(rest)-1], " ")), true
	case role == RoleTeam && (cmd.Name == "help" || cmd.Name == "menu" || cmd.Name == "start"):
		return TeamHelpText, true
	}
	return "", false
}

func isDoneWord(w string) bool {
	switch w {
	case "done", "complete", "completed":
		return true
	}
	return false
}

func parseNumbers(s string) []int {
	var out []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// member finds the ClickUp member a contact's name refers to.
func (r *Router) memberOf(ctx context.Context, p Contact) (clickup.Member, bool, error) {
	if p.Name == "" {
		return clickup.Member{}, false, nil
	}
	members, err := r.ClickUp.Members(ctx)
	if err != nil {
		return clickup.Member{}, false, err
	}
	found := MatchMembers(members, p.Name)
	if len(found) != 1 {
		return clickup.Member{}, false, nil
	}
	return found[0], true, nil
}

func notLinked(role Role) string {
	if role == RoleAdmin {
		return "Your number has no ClickUp name saved, so I don't know which tasks are yours. Add it in ADMIN_WA_NUMBERS on the server, e.g. suranjana=9779812345678."
	}
	return "Your number isn't linked to a ClickUp name yet. Ask the project manager to add it."
}

// openTasks returns m's unfinished tasks, earliest deadline first.
func (r *Router) openTasks(ctx context.Context, m clickup.Member) ([]clickup.Task, error) {
	tasks, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{Assignees: []int{m.ID}})
	if err != nil {
		return nil, err
	}
	var open []clickup.Task
	for _, t := range tasks {
		if !t.IsDone() {
			open = append(open, t)
		}
	}
	sortByDue(open)
	return open, nil
}

// --- my tasks ---

func (r *Router) myTasks(ctx context.Context, p Contact, role Role) string {
	m, ok, err := r.memberOf(ctx, p)
	if err != nil {
		return r.clickupFailed(err)
	}
	if !ok {
		return notLinked(role)
	}
	tasks, err := r.openTasks(ctx, m)
	if err != nil {
		return r.clickupFailed(err)
	}
	now := r.now()
	if len(tasks) > maxListed {
		tasks = tasks[:maxListed]
	}
	r.sessions.with(p.Number, func(s *session) { s.list, s.listAt = tasks, now })
	return MyTasksReport(tasks, now)
}

// MyTasksReport is the numbered "my tasks" list. now must be in the bot's
// time zone.
func MyTasksReport(tasks []clickup.Task, now time.Time) string {
	var b strings.Builder
	header(&b, "📋 Your tasks", now)
	if len(tasks) == 0 {
		b.WriteString("\nNo open tasks. 🎉")
		return b.String()
	}
	today := dateOf(now, now.Location())
	group := func(t clickup.Task) string {
		switch {
		case t.DueDate == nil:
			return "🗂 No due date"
		case dateOf(*t.DueDate, now.Location()).Before(today):
			return "⚠️ Overdue"
		case dateOf(*t.DueDate, now.Location()).Equal(today):
			return "📌 Due today"
		default:
			return "🔜 Upcoming"
		}
	}
	current := ""
	for i, t := range tasks {
		if g := group(t); g != current {
			current = g
			b.WriteString("\n*" + g + "*\n")
		}
		b.WriteString(numbered(i+1, t, now) + "\n")
	}
	b.WriteString("\n_Reply a number for the brief and links, or_ *done 2, 3* _when finished._")
	return b.String()
}

// numbered is one list line: "*2* WeeTutor · _due today_ 💬".
func numbered(n int, t clickup.Task, now time.Time) string {
	line := fmt.Sprintf("*%d* %s · _%s_", n, truncate(t.Name, 60), strings.ToLower(dueText(t, now)[:1])+dueText(t, now)[1:])
	if t.HasBrief {
		line += " 💬"
	}
	return line
}

// --- numbers ---

// pick answers a reply that is only numbers: the choice after an ambiguous
// done, or a task's details from the last "my tasks" list.
func (r *Router) pick(ctx context.Context, p Contact, role Role, nums []int) string {
	now := r.Now()
	var pending, list []clickup.Task
	r.sessions.with(p.Number, func(s *session) {
		if fresh(s.pendingAt, now, pendingTTL) {
			pending = s.pending
		}
		if fresh(s.listAt, now, listTTL) {
			list = s.list
		}
	})
	switch {
	case pending != nil:
		return r.completeNumbers(ctx, p, pending, nums)
	case list != nil && len(nums) == 1:
		return r.details(ctx, list, nums[0])
	case list != nil:
		return fmt.Sprintf("To mark these complete, send *done %s*. To see one task, send just its number.", joinNumbers(nums))
	case role == RoleAdmin:
		return "Send *my tasks* first, then reply with a task's number."
	}
	return "" // a team member's "2" in ordinary chat
}

func joinNumbers(nums []int) string {
	s := make([]string, len(nums))
	for i, n := range nums {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// --- details ---

func (r *Router) details(ctx context.Context, list []clickup.Task, n int) string {
	if n < 1 || n > len(list) {
		return fmt.Sprintf("There's no task %d in your list. Send *my tasks* to see it again.", n)
	}
	t := list[n-1]
	d, err := r.ClickUp.TaskDetail(ctx, t.ID)
	if err != nil {
		return r.clickupFailed(err)
	}
	admins, err := r.adminMemberIDs(ctx)
	if err != nil {
		return r.clickupFailed(err)
	}
	var comments []clickup.Comment
	for _, c := range d.Comments {
		if admins[c.Author.ID] && !strings.Contains(c.Text, whatsappMarker) && strings.TrimSpace(c.Text) != "" {
			comments = append(comments, c)
		}
	}
	return TaskDetails(t, d.Description, comments, n, r.now())
}

// TaskDetails is one task's brief, links and comments. now must be in the
// bot's time zone.
func TaskDetails(t clickup.Task, description string, comments []clickup.Comment, n int, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*📌 %s*\n_%s · %s_\n", t.Name, dueText(t, now), titleCase(t.Status))
	if brief := whatsappText(description); brief != "" {
		b.WriteString("\n*Brief*\n" + truncate(brief, maxBrief) + "\n")
	} else {
		b.WriteString("\n_No brief or assets in ClickUp. Ask the project manager._\n")
	}
	if len(comments) > 0 {
		b.WriteString("\n*💬 Comments*\n")
		if older := len(comments) - maxComments; older > 0 {
			fmt.Fprintf(&b, "_+%d older in ClickUp_\n", older)
			comments = comments[older:]
		}
		for _, c := range comments {
			name := c.Author.DisplayName()
			if first, _, ok := strings.Cut(name, " "); ok {
				name = first
			}
			fmt.Fprintf(&b, "_%s · %s · %s_\n%s\n", name, shortDate(c.Date, now), stamp(c.Date.In(now.Location())), truncate(whatsappText(c.Text), 600))
		}
	}
	if t.URL != "" {
		b.WriteString("\n" + t.URL + "\n")
	}
	fmt.Fprintf(&b, "\n_Reply_ *done %d* _when it's finished._", n)
	return b.String()
}

// adminMemberIDs are the ClickUp users whose comments count as instructions:
// admins with a ClickUp name, and the owner of the bot's ClickUp token.
func (r *Router) adminMemberIDs(ctx context.Context) (map[int]bool, error) {
	ids := map[int]bool{}
	me, err := r.ClickUp.Me(ctx)
	if err != nil {
		return nil, err
	}
	ids[me.ID] = true
	for _, a := range r.Admins {
		m, ok, err := r.memberOf(ctx, a)
		if err != nil {
			return nil, err
		}
		if ok {
			ids[m.ID] = true
		}
	}
	return ids, nil
}

// --- done ---

func (r *Router) done(ctx context.Context, p Contact, role Role, args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return "Which task? Send *done weetutor*, or *my tasks* and then *done 2, 3*."
	}
	if numbersOnly.MatchString(args) {
		now := r.Now()
		var choices []clickup.Task
		r.sessions.with(p.Number, func(s *session) {
			switch {
			case fresh(s.pendingAt, now, pendingTTL):
				choices = s.pending
			case fresh(s.listAt, now, listTTL):
				choices = s.list
			}
		})
		if choices == nil {
			return "Send *my tasks* first, then *done* with the numbers, e.g. *done 2, 3*."
		}
		return r.completeNumbers(ctx, p, choices, parseNumbers(args))
	}

	members, err := r.ClickUp.Members(ctx)
	if err != nil {
		return r.clickupFailed(err)
	}
	query, target, onBehalf := args, clickup.Member{}, false
	if role == RoleAdmin {
		query, target, onBehalf = splitOwner(members, args)
	}
	if !onBehalf {
		m, ok, err := r.memberOf(ctx, p)
		if err != nil {
			return r.clickupFailed(err)
		}
		if !ok {
			if role == RoleAdmin {
				return "Whose task is it? Send e.g. *done weetutor for sagun*."
			}
			return notLinked(role)
		}
		target = m
	}

	tasks, err := r.openTasks(ctx, target)
	if err != nil {
		return r.clickupFailed(err)
	}
	var matches []clickup.Task
	for _, t := range tasks {
		if fuzzyContains(t.Name, query) {
			matches = append(matches, t)
		}
	}
	whose := "you"
	if onBehalf {
		whose = firstName(target)
	}
	switch len(matches) {
	case 0:
		return fmt.Sprintf("I can't find an open task called *%s* for %s. Send *my tasks* to see the list.", query, whose)
	case 1:
		return r.complete(ctx, p, matches)
	}
	now := r.now()
	r.sessions.with(p.Number, func(s *session) { s.pending, s.pendingAt = matches, r.Now() })
	who := "You have"
	if onBehalf {
		who = firstName(target) + " has"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d open tasks matching *%s*. Which are done?\n\n", who, len(matches), query)
	for i, t := range matches {
		b.WriteString(numbered(i+1, t, now) + "\n")
	}
	b.WriteString("\n_Reply with the number, or several like_ *1, 3*")
	return b.String()
}

// splitOwner reads "weetutor for sagun" or "weetutor sagun": a trailing name
// that is exactly one member's first name means the task is theirs.
func splitOwner(members []clickup.Member, args string) (query string, owner clickup.Member, ok bool) {
	words := strings.Fields(args)
	if len(words) < 2 {
		return args, clickup.Member{}, false
	}
	name := strings.ToLower(words[len(words)-1])
	rest := words[:len(words)-1]
	if len(rest) > 1 && strings.EqualFold(rest[len(rest)-1], "for") {
		rest = rest[:len(rest)-1]
	}
	var found []clickup.Member
	for _, m := range members {
		first, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(m.Username)), " ")
		if first == name {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		return args, clickup.Member{}, false
	}
	return strings.Join(rest, " "), found[0], true
}

// fuzzyContains matches task names forgivingly: case, spaces, punctuation and
// doubled letters are ignored, so "wetutor" finds "WeeTutor".
func fuzzyContains(name, query string) bool {
	q := squash(query)
	return q != "" && strings.Contains(squash(name), q)
}

func squash(s string) string {
	var b strings.Builder
	var last rune
	for _, r := range strings.ToLower(s) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			continue
		}
		if r != last {
			b.WriteRune(r)
		}
		last = r
	}
	return b.String()
}

func (r *Router) completeNumbers(ctx context.Context, p Contact, choices []clickup.Task, nums []int) string {
	var picked []clickup.Task
	var skipped []string
	seen := map[int]bool{}
	for _, n := range nums {
		switch {
		case seen[n]:
		case n < 1 || n > len(choices):
			skipped = append(skipped, strconv.Itoa(n))
		default:
			picked = append(picked, choices[n-1])
		}
		seen[n] = true
	}
	if len(picked) == 0 {
		return fmt.Sprintf("There's no task %s in the list. Send *my tasks* to see it again.", strings.Join(skipped, ", "))
	}
	reply := r.complete(ctx, p, picked)
	if len(skipped) > 0 {
		reply += fmt.Sprintf("\n\n_Skipped %s: not in the list._", strings.Join(skipped, ", "))
	}
	return reply
}

// complete marks tasks complete in ClickUp, leaves a comment saying who did
// it, remembers them for undo and tells the other admins.
func (r *Router) complete(ctx context.Context, p Contact, tasks []clickup.Task) string {
	actor := r.actorName(ctx, p)
	now := r.now()
	var done []clickup.Task
	var failed []string
	for _, t := range tasks {
		if err := r.setDone(ctx, t); err != nil {
			r.Log.Error("done: clickup update failed", "task", t.ID, "err", err)
			failed = append(failed, t.Name)
			continue
		}
		note := "✅ Marked complete by " + actor
		if owner := ownerName(t); owner != "" && !strings.EqualFold(owner, actor) {
			note += " on behalf of " + owner
		}
		note += " " + whatsappMarker + " · " + shortDate(now, now) + " · " + stamp(now)
		if err := r.ClickUp.AddComment(ctx, t.ID, note); err != nil {
			r.Log.Warn("done: could not comment on task", "task", t.ID, "err", err)
		}
		done = append(done, t)
	}
	if len(done) > 0 {
		r.sessions.with(p.Number, func(s *session) {
			s.undo, s.undoAt = done, r.Now()
			s.pending, s.pendingAt = nil, time.Time{}
		})
		r.Log.Info("done: tasks completed", "by_suffix", suffix(p.Number), "count", len(done))
		r.tellAdmins(ctx, p, actor, done, now)
	}

	var b strings.Builder
	if len(done) == 1 {
		fmt.Fprintf(&b, "✅ Marked *%s* (%s) complete.", done[0].Name, strings.ToLower(dueText(done[0], now)))
	} else if len(done) > 1 {
		fmt.Fprintf(&b, "✅ Marked %d tasks complete:\n", len(done))
		for _, t := range done {
			fmt.Fprintf(&b, "• %s · _%s_\n", t.Name, strings.ToLower(dueText(t, now)))
		}
	}
	if len(failed) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "⚠️ Couldn't update %s in ClickUp. Try again in a minute.", strings.Join(failed, ", "))
	}
	if len(done) > 0 {
		b.WriteString("\n_Mistake? Send_ *undo* _within 10 minutes._")
	}
	return strings.TrimSpace(b.String())
}

// setDone moves t to its list's done status. A list's "closed" status is used
// only when it has no "done" one and isn't a cancellation.
func (r *Router) setDone(ctx context.Context, t clickup.Task) error {
	statuses, err := r.ClickUp.ListStatuses(ctx, t.ListID)
	if err != nil {
		return err
	}
	status := ""
	for _, s := range statuses {
		if s.Type == "done" {
			status = s.Name
			break
		}
	}
	if status == "" {
		for _, s := range statuses {
			if s.Type == "closed" && !strings.Contains(strings.ToLower(s.Name), "cancel") {
				status = s.Name
				break
			}
		}
	}
	if status == "" {
		return fmt.Errorf("list %s has no complete status", t.ListID)
	}
	return r.ClickUp.SetStatus(ctx, t.ID, status)
}

// tellAdmins messages the other admins about completed tasks, but only those
// whose 24 hour window is open, so it never costs anything.
func (r *Router) tellAdmins(ctx context.Context, p Contact, actor string, done []clickup.Task, now time.Time) {
	var b strings.Builder
	fmt.Fprintf(&b, "✅ *%s* completed:\n", actor)
	for _, t := range done {
		line := "• " + t.Name + " · _" + strings.ToLower(dueText(t, now)) + "_"
		if owner := ownerName(t); owner != "" && !strings.EqualFold(owner, actor) {
			line += " · for " + owner
		}
		b.WriteString(line + "\n")
	}
	msg := strings.TrimSpace(b.String())
	for _, a := range r.Admins {
		if a.Number == p.Number || !r.windowOpen(a.Number) {
			continue
		}
		if err := r.Messenger.SendText(ctx, a.Number, msg); err != nil {
			r.Log.Warn("done: could not tell admin", "to_suffix", suffix(a.Number), "err", err)
		}
	}
}

// actorName is the sender's ClickUp first name, or their saved name.
func (r *Router) actorName(ctx context.Context, p Contact) string {
	if m, ok, err := r.memberOf(ctx, p); err == nil && ok {
		return firstName(m)
	}
	if p.Name != "" {
		return titleCase(p.Name)
	}
	return "an admin"
}

// ownerName is the first assignee's first name.
func ownerName(t clickup.Task) string {
	if names := t.AssigneeNames(); len(names) > 0 {
		return names[0]
	}
	return ""
}

// --- undo ---

func (r *Router) undo(ctx context.Context, p Contact) string {
	now := r.Now()
	var tasks []clickup.Task
	r.sessions.with(p.Number, func(s *session) {
		if fresh(s.undoAt, now, undoTTL) {
			tasks = s.undo
		}
		s.undo, s.undoAt = nil, time.Time{}
	})
	if len(tasks) == 0 {
		return "Nothing to undo. Undo works for 10 minutes after *done*."
	}
	actor := r.actorName(ctx, p)
	local := r.now()
	var reopened, failed []string
	for _, t := range tasks {
		if err := r.ClickUp.SetStatus(ctx, t.ID, t.Status); err != nil {
			r.Log.Error("undo: clickup update failed", "task", t.ID, "err", err)
			failed = append(failed, t.Name)
			continue
		}
		note := "↩️ Reopened by " + actor + " " + whatsappMarker + " (undo) · " + shortDate(local, local) + " · " + stamp(local)
		if err := r.ClickUp.AddComment(ctx, t.ID, note); err != nil {
			r.Log.Warn("undo: could not comment on task", "task", t.ID, "err", err)
		}
		reopened = append(reopened, t.Name)
	}
	sort.Strings(reopened)
	reply := ""
	if len(reopened) > 0 {
		reply = "↩️ Reopened " + strings.Join(reopened, ", ") + "."
	}
	if len(failed) > 0 {
		reply = strings.TrimSpace(reply + "\n⚠️ Couldn't reopen " + strings.Join(failed, ", ") + " in ClickUp. Check it there.")
	}
	return reply
}
