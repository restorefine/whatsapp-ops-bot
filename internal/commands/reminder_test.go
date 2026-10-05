package commands

import (
	"context"
	"testing"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

func reminderTasks() []clickup.Task {
	return append(uploadTasks(),
		task("d1", "Send invoice draft", "to do", "open", at(16, time.October, 17), alice),
		task("d2", "Already shipped", "complete", "closed", at(16, time.October, 9), bob),
		upload("u9", "Masala(G)", "Masala", "open", at(16, time.October, 9)),
	)
}

func TestDueCommand(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team, tasks: reminderTasks()})
	r.Uploads = "Uploads"
	got := r.Reply(context.Background(), "/due")
	mustContain(t, got,
		"*📌 Due today* (1)", "• Client call notes", "Bob · In Progress",
		"*📤 Posting today* (1)", "⏳ WeTutor(V)",
		"*🔜 Due tomorrow · Fri 16 Oct* (1)", "• Send invoice draft",
		"*📤 Posting tomorrow* (1)", "⏳ Masala(G)",
		"1 older task is still overdue", // t1; missed posts are not counted here
	)
	mustNotContain(t, got, "Already shipped", "Write case study", "Masala(V)", "Unassigned")
}

func TestDueCommandNothingDue(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team})
	mustContain(t, r.Reply(context.Background(), "today"), "Nothing due today or tomorrow")
}

func TestNextRun(t *testing.T) {
	tests := []struct {
		now  time.Time
		want time.Time
	}{
		{time.Date(2026, 10, 15, 7, 59, 0, 0, ktm), time.Date(2026, 10, 15, 8, 0, 0, 0, ktm)},
		{time.Date(2026, 10, 15, 8, 0, 0, 0, ktm), time.Date(2026, 10, 16, 8, 0, 0, 0, ktm)},
		{time.Date(2026, 10, 31, 23, 0, 0, 0, ktm), time.Date(2026, 11, 1, 8, 0, 0, 0, ktm)},
	}
	for _, tt := range tests {
		if got := NextRun(tt.now, 8, 0); !got.Equal(tt.want) {
			t.Errorf("NextRun(%v) = %v, want %v", tt.now, got, tt.want)
		}
	}
}

// windowMessenger rejects free-form text to closed numbers, like the Graph API.
type windowMessenger struct {
	fakeMessenger
	closed    map[string]bool
	templates []sent
	params    []string
}

func (m *windowMessenger) SendText(ctx context.Context, to, body string) error {
	if m.closed[to] {
		return &whatsapp.APIError{HTTPStatus: 400, Code: whatsapp.CodeReengagement}
	}
	return m.fakeMessenger.SendText(ctx, to, body)
}

func (m *windowMessenger) SendTemplate(_ context.Context, to, name, _ string, params []string) error {
	m.templates = append(m.templates, sent{to, name})
	m.params = params
	return nil
}

func TestReminderFallsBackToTemplate(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team, tasks: reminderTasks()})
	r.Uploads = "Uploads"
	m := &windowMessenger{closed: map[string]bool{"222": true}}
	r.Messenger = m

	rem := &Reminder{Router: r, Owners: []string{"111", "222"}, Template: "due_reminder", Lang: "en"}
	rem.Send(context.Background())

	if len(m.sent) != 1 || m.sent[0].to != "111" {
		t.Fatalf("text sent to %+v, want only 111", m.sent)
	}
	mustContain(t, m.sent[0].body, "⏰ Due soon", "Send invoice draft")
	if len(m.templates) != 1 || m.templates[0] != (sent{"222", "due_reminder"}) {
		t.Fatalf("templates = %+v", m.templates)
	}
	// Today: Client call notes, WeTutor(V). Tomorrow: Send invoice draft, Masala(G).
	if m.params[0] != "2" || m.params[1] != "2" {
		t.Errorf("template params = %v, want [2 2]", m.params)
	}
}

func TestReminderWithoutTemplateSkipsClosedWindow(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{members: team, tasks: reminderTasks()})
	m := &windowMessenger{closed: map[string]bool{"222": true}}
	r.Messenger = m

	(&Reminder{Router: r, Owners: []string{"111", "222"}}).Send(context.Background())
	if len(m.sent) != 1 || len(m.templates) != 0 {
		t.Fatalf("sent %+v, templates %+v", m.sent, m.templates)
	}
}
