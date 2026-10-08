package commands

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

// fakeClickUp applies TaskFilter roughly the way the ClickUp API does.
type fakeClickUp struct {
	members  []clickup.Member
	tasks    []clickup.Task
	err      error
	me       clickup.Member
	details  map[string]clickup.TaskDetail
	statuses []clickup.Status // every list's statuses; default to-do / complete / cancelled
	setErr   error
	comments map[string][]string // task ID → comments added
}

func (f *fakeClickUp) Members(context.Context) ([]clickup.Member, error) { return f.members, f.err }

func (f *fakeClickUp) TaskDetail(_ context.Context, id string) (clickup.TaskDetail, error) {
	return f.details[id], f.err
}

func (f *fakeClickUp) ListStatuses(context.Context, string) ([]clickup.Status, error) {
	if f.statuses != nil {
		return f.statuses, f.err
	}
	return []clickup.Status{{Name: "to do", Type: "open"}, {Name: "complete", Type: "done"}, {Name: "cancelled", Type: "closed"}}, f.err
}

// SetStatus updates the stored task, treating "complete" as done.
func (f *fakeClickUp) SetStatus(_ context.Context, id, status string) error {
	if f.setErr != nil {
		return f.setErr
	}
	for i := range f.tasks {
		if f.tasks[i].ID == id {
			f.tasks[i].Status, f.tasks[i].StatusType = status, "open"
			if status == "complete" {
				f.tasks[i].StatusType = "done"
			}
		}
	}
	return nil
}

func (f *fakeClickUp) AddComment(_ context.Context, id, text string) error {
	if f.comments == nil {
		f.comments = map[string][]string{}
	}
	f.comments[id] = append(f.comments[id], text)
	return nil
}

func (f *fakeClickUp) Me(context.Context) (clickup.Member, error) { return f.me, f.err }

func (f *fakeClickUp) Tasks(_ context.Context, filter clickup.TaskFilter) ([]clickup.Task, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []clickup.Task
	for _, t := range f.tasks {
		if !filter.IncludeClosed && t.StatusType == "closed" {
			continue
		}
		if !filter.DueAfter.IsZero() && (t.DueDate == nil || !t.DueDate.After(filter.DueAfter)) {
			continue
		}
		if !filter.DueBefore.IsZero() && (t.DueDate == nil || !t.DueDate.Before(filter.DueBefore)) {
			continue
		}
		if !filter.UpdatedAfter.IsZero() && !t.DateUpdated.After(filter.UpdatedAfter) {
			continue
		}
		if len(filter.Assignees) > 0 {
			match := false
			for _, id := range filter.Assignees {
				match = match || t.AssignedTo(id)
			}
			if !match {
				continue
			}
		}
		out = append(out, t)
	}
	return out, nil
}

type sent struct{ to, body string }

type fakeMessenger struct {
	mu   sync.Mutex
	sent []sent
}

func (m *fakeMessenger) SendText(_ context.Context, to, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sent{to, body})
	return nil
}

func (m *fakeMessenger) SendTemplate(context.Context, string, string, string, []string) error {
	return nil
}

var (
	ktm, _     = time.LoadLocation("Asia/Kathmandu")
	now        = time.Date(2026, 10, 15, 14, 0, 0, 0, ktm) // Thu 15 Oct 2026
	alice      = clickup.Member{ID: 1, Username: "Alice Smith"}
	aliceJones = clickup.Member{ID: 2, Username: "Alice Jones"}
	bob        = clickup.Member{ID: 3, Username: "Bob Stone"}
	anna       = clickup.Member{ID: 4, Username: "Anna Lee"}
	carol      = clickup.Member{ID: 5, Email: "carol@example.com"}
	team       = []clickup.Member{alice, aliceJones, bob, anna, carol}
)

func at(day int, month time.Month, hour int) *time.Time {
	t := time.Date(2026, month, day, hour, 0, 0, 0, ktm)
	return &t
}

func task(id, name, status, typ string, due *time.Time, who ...clickup.Member) clickup.Task {
	t := clickup.Task{ID: id, Name: name, Status: status, StatusType: typ, DueDate: due, Assignees: who, DateUpdated: now.Add(-time.Hour), ListName: "Website"}
	if typ == "closed" {
		t.DateClosed = at(10, time.October, 12)
	}
	return t
}

func sampleTasks() []clickup.Task {
	lastMonthDone := task("old", "Old launch", "complete", "closed", at(20, time.September, 9), bob)
	lastMonthDone.DateClosed = at(20, time.September, 12)
	lastMonthDone.DateUpdated = *at(20, time.September, 12)

	// 20:00 UTC on 14 Oct is 01:45 on 15 Oct in Kathmandu: due today, not overdue.
	tzEdge := time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)

	return []clickup.Task{
		task("t1", "Fix checkout bug", "in progress", "custom", at(12, time.October, 9), bob),
		task("t2", "Write case study", "to do", "open", at(20, time.October, 9), bob),
		task("t3", "Ship landing page", "complete", "closed", at(9, time.October, 9), bob),
		task("t4", "Client call notes", "in progress", "custom", &tzEdge, bob),
		task("t5", "Plan Q4 roadmap", "to do", "open", at(3, time.November, 9), alice),
		task("t6", "Year end review", "to do", "open", at(5, time.December, 9), bob),
		task("t7", "Backlog idea", "to do", "open", nil, bob),
		task("t8", "Alice's task", "to do", "open", at(18, time.October, 9), alice),
		lastMonthDone,
	}
}

func newRouter(cu *fakeClickUp) (*Router, *fakeMessenger) {
	m := &fakeMessenger{}
	return &Router{
		ClickUp:   cu,
		Messenger: m,
		Loc:       ktm,
		Now:       func() time.Time { return now },
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, m
}

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want Command
	}{
		{"/help", Command{"help", ""}},
		{"  /HELP!! ", Command{"help", ""}},
		{"/Update.", Command{"update", ""}},
		{"update", Command{"update", ""}},
		{"/Alice   Smith?", Command{"alice", "Smith"}},
		{"/a", Command{"a", ""}},
		{"/", Command{"", ""}},
		{"", Command{"", ""}},
	}
	for _, tt := range tests {
		if got := Parse(tt.in); got != tt.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestMatchMembers(t *testing.T) {
	tests := []struct {
		query string
		want  []int
	}{
		{"alice", []int{2, 1}}, // two Alices: ambiguous, sorted by name
		{"alicesmith", []int{1}},
		{"alice smith", []int{1}},
		{"ALICE JONES", []int{2}},
		{"a", []int{2, 1, 4}}, // prefix of first names
		{"bob", []int{3}},
		{"sto", []int{3}}, // prefix of surname
		{"carol", []int{5}},
		{"zed", nil},
		{"", nil},
	}
	for _, tt := range tests {
		got := MatchMembers(team, tt.query)
		var ids []int
		for _, m := range got {
			ids = append(ids, m.ID)
		}
		if len(ids) != len(tt.want) {
			t.Errorf("%q: got %v, want %v", tt.query, ids, tt.want)
			continue
		}
		for i := range ids {
			if ids[i] != tt.want[i] {
				t.Errorf("%q: got %v, want %v", tt.query, ids, tt.want)
				break
			}
		}
	}
}

func TestShortCommand(t *testing.T) {
	want := map[int]string{1: "/alicesmith", 2: "/alicejones", 3: "/bob", 4: "/anna", 5: "/carol"}
	for _, m := range team {
		if got := ShortCommand(team, m); got != want[m.ID] {
			t.Errorf("%s: got %s, want %s", m.DisplayName(), got, want[m.ID])
		}
		// Every suggested command must select exactly that member.
		if found := MatchMembers(team, strings.TrimPrefix(ShortCommand(team, m), "/")); len(found) != 1 || found[0].ID != m.ID {
			t.Errorf("%s does not resolve uniquely", ShortCommand(team, m))
		}
	}
}

func TestShortCommandSameName(t *testing.T) {
	work := clickup.Member{ID: 10, Username: "Prabish Dangi", Email: "prabish@example.com"}
	personal := clickup.Member{ID: 11, Username: "prabish dangi", Email: "prabish.home@example.com"}
	members := []clickup.Member{work, personal, bob}
	want := map[int]string{10: "/prabish", 11: "/prabish.home"}
	for _, m := range []clickup.Member{work, personal} {
		got := ShortCommand(members, m)
		if got != want[m.ID] {
			t.Errorf("%d: got %s, want %s", m.ID, got, want[m.ID])
		}
		if found := MatchMembers(members, Parse(got).Full()); len(found) != 1 || found[0].ID != m.ID {
			t.Errorf("%s does not resolve to member %d", got, m.ID)
		}
	}
}

func TestProcessRepliesToSender(t *testing.T) {
	r, m := newRouter(&fakeClickUp{members: team})
	r.Admins = []Contact{{Number: "977"}}
	r.Process(context.Background(), whatsapp.InboundMessage{ID: "w1", From: "977", Type: "text", Text: "/HELP"})
	r.Process(context.Background(), whatsapp.InboundMessage{ID: "w2", From: "977", Type: "image"})
	r.Process(context.Background(), whatsapp.InboundMessage{ID: "w3", From: "15550001111", Type: "text", Text: "/help"})
	if len(m.sent) != 2 {
		t.Fatalf("sent %d messages", len(m.sent))
	}
	if m.sent[0].to != "977" || m.sent[0].body != HelpText {
		t.Errorf("help reply = %+v", m.sent[0])
	}
	if m.sent[1].body != NonTextReply {
		t.Errorf("non-text reply = %q", m.sent[1].body)
	}
}

func TestMemberReport(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team, tasks: sampleTasks()})
	got := r.Reply(context.Background(), "/bob")

	mustContain(t, got,
		"*👤 Bob Stone*\n_Thu 15 Oct 2026 · 14:00_",
		"```\nOpen tasks           5\nOverdue              1\nDue in next 7 days   2\nCompleted in Oct     1\n```",
		"*⚠️ Overdue* (1)\n• Fix checkout bug\n   _Due Mon 12 Oct · 3 days late · In Progress · Website_",
		"*🔹 In Progress* (1)\n• Client call notes\n   _Due today · Website_",
		"*🔹 To Do* (3)\n• Write case study\n   _Due Tue 20 Oct · Website_\n• Year end review\n   _Due Sat 5 Dec · Website_\n• Backlog idea\n   _No due date · Website_",
		"*✅ Completed in October* (1)\n• Ship landing page\n   _Completed Sat 10 Oct · Website_",
	)
	mustNotContain(t, got, "Old launch", "Alice's task", "Plan Q4")

	// In progress comes before To Do.
	if strings.Index(got, "In Progress*") > strings.Index(got, "To Do*") {
		t.Error("custom statuses should be listed before open statuses")
	}
}

func TestMemberAmbiguousAndUnknown(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team})
	mustContain(t, r.Reply(context.Background(), "/alice"), "*/alice* matches 2 people", "*/alicejones*  Alice Jones", "*/alicesmith*  Alice Smith")
	mustContain(t, r.Reply(context.Background(), "/zed"), "I don't recognise */zed*", HelpText)
}

func TestMemberNothingAssigned(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team})
	mustContain(t, r.Reply(context.Background(), "/anna"), "*👤 Anna Lee*", "No open tasks")
}

func TestMonthReport(t *testing.T) {
	tasks := append(sampleTasks(), task("t9", "Video edit", "in progress", "custom", at(28, time.September, 9), alice))
	r, _ := newRouter(&fakeClickUp{members: team, tasks: tasks})
	got := r.Reply(context.Background(), "/update")

	mustContain(t, got,
		"*📊 Monthly Update · October 2026*",
		"```\nDue this month   5\nCompleted        1\nStill open       3\nOverdue          1\nOverdue, older   1\n```",
		"*Team*\n```\nName   Open  Late  Done\nBob       5     1     1\nAlice     3     1     0\n```",
		"*⚠️ Overdue from earlier months* (1)\n• Video edit\n   _Alice · 17 days late · In Progress_",
		"*Fri 9 Oct*\n✅ Ship landing page · Bob\n",
		"*Mon 12 Oct*\n⚠️ Fix checkout bug · Bob · _In Progress_\n",
		"*Thu 15 Oct* · _today_\n⏳ Client call notes · Bob · _In Progress_\n",
		"*Sun 18 Oct*\n⏳ Alice's task · Alice · _To Do_\n",
		"*🔭 Coming up · November 2026*\n1 task due",
		"*Tue 3 Nov*\n⏳ Plan Q4 roadmap · Alice · _To Do_",
	)
	mustNotContain(t, got, "Year end review", "Old launch", "Backlog idea", "Social uploads")

	// Calendar days are in order.
	if strings.Index(got, "Fri 9 Oct") > strings.Index(got, "Tue 20 Oct") {
		t.Error("calendar is not in date order")
	}
}

func TestMonthReportEmpty(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team})
	got := r.Reply(context.Background(), "/update")
	mustContain(t, got, "Nothing due this month.", "*🔭 Coming up · November 2026*\nNothing scheduled yet.")
	mustNotContain(t, got, "*Team*")
}

func TestTeam(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team})
	mustContain(t, r.Reply(context.Background(), "/team"), "*👥 Team* (5)", "*/bob*  Bob Stone", "*/carol*  carol@example.com")
}

func TestClickUpErrorIsReported(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{err: errors.New("clickup: http 401: Token invalid")})
	for _, cmd := range []string{"/update", "/bob", "/team"} {
		mustContain(t, r.Reply(context.Background(), cmd), "Couldn't fetch data from ClickUp", "Token invalid")
	}
}

func TestDueTextAcrossTimezoneBoundary(t *testing.T) {
	// 18:30 UTC on 14 Oct is 00:15 on 15 Oct in Kathmandu.
	due := time.Date(2026, 10, 14, 18, 30, 0, 0, time.UTC)
	tk := clickup.Task{DueDate: &due}
	if got := dueText(tk, now); got != "Due today" {
		t.Errorf("in Kathmandu: got %q, want Due today", got)
	}
	// The same instant is the day before in UTC.
	if got := dueText(tk, now.In(time.UTC)); got != "Due Wed 14 Oct · 1 day late" {
		t.Errorf("in UTC: got %q", got)
	}
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("reply is missing %q\n--- reply ---\n%s", w, got)
		}
	}
}

func mustNotContain(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(got, u) {
			t.Errorf("reply should not contain %q\n--- reply ---\n%s", u, got)
		}
	}
}

func upload(id, name, list, typ string, due *time.Time) clickup.Task {
	t := task(id, name, "to do", typ, due)
	if typ == "closed" {
		t.Status = "complete"
	}
	t.ListName, t.FolderName = list, "Uploads"
	return t
}

func uploadTasks() []clickup.Task {
	return append(sampleTasks(),
		upload("u1", "WeTutor(G)", "WeTutor", "closed", at(2, time.October, 9)),
		upload("u2", "Masala(V)", "Masala", "open", at(2, time.October, 9)),      // missed
		upload("u3", "WeTutor(V)", "WeTutor", "open", at(15, time.October, 9)),   // today
		upload("u4", "Reel", "Damasqino", "open", at(18, time.October, 9)),       // name gets client prefix
		upload("u5", "Failte(G)", "failte", "open", at(28, time.September, 9)),   // missed last month
		upload("u6", "Failte(V)", "failte", "closed", at(27, time.September, 9)), // posted last month
		upload("u7", "Zaks(G)", "Zaks Drive thru", "open", at(4, time.November, 9)),
		upload("u8", "Fav Spot (V) 8", "FavSpot", "open", at(5, time.November, 9)),
	)
}

func TestUpdateSummarisesUploads(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team, tasks: uploadTasks()})
	r.Uploads = "Uploads"
	got := r.Reply(context.Background(), "/update")

	mustContain(t, got,
		"Due this month   5",      // team work only
		"Bob       5     1     1", // uploads are not in the team table
		"*📤 Social uploads*\n```\nScheduled   4\nPosted      1\nMissed      1\nUpcoming    2\n```",
		"Today: ⏳ WeTutor(V)",
		"Send /uploads",
	)
	mustNotContain(t, got, "Masala(V)", "Failte(G)", "Zaks(G)", "Unassigned", "earlier months")
}

func TestUploadsCalendar(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team, tasks: uploadTasks()})
	r.Uploads = "Uploads"
	got := r.Reply(context.Background(), "/uploads")

	mustContain(t, got,
		"*📤 Uploads · October 2026*",
		"```\nScheduled   4\nPosted      1\nMissed      1\nUpcoming    2\n```",
		"*⚠️ Not marked as posted, from earlier* (1)\n• Failte(G)\n   _Due Mon 28 Sep · 17 days late_",
		"*Fri 2 Oct*\n⚠️ Masala(V)\n✅ WeTutor(G)\n",
		"*Thu 15 Oct* · _today_\n⏳ WeTutor(V)\n",
		"*Sun 18 Oct*\n⏳ Damasqino: Reel",
	)
	mustNotContain(t, got, "Fix checkout bug", "Failte(V)", "Zaks(G)")

	next := r.Reply(context.Background(), "/uploads next")
	mustContain(t, next, "*📤 Uploads · November 2026*", "Scheduled   2", "*Wed 4 Nov*\n⏳ Zaks(G)", "*Thu 5 Nov*\n⏳ Fav Spot (V) 8")
	mustNotContain(t, next, "Not marked as posted", "WeTutor")
}

func TestUploadsDisabled(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team})
	mustContain(t, r.Reply(context.Background(), "/uploads"), "turned off")
}
