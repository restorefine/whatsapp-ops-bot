package commands

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

const bobNumber = "447700900456"

func remindRouter(t *testing.T) (*Router, *fakeMessenger) {
	t.Helper()
	post := task("p1", "Diwali reel", "to do", "open", at(15, time.October, 18), bob)
	post.FolderName, post.ListName = "Uploads", "Zaks"
	cu := &fakeClickUp{members: team, tasks: []clickup.Task{
		task("b1", "Fix checkout bug", "in progress", "custom", at(15, time.October, 9), bob),
		task("b2", "Done already", "complete", "closed", at(15, time.October, 9), bob),
		task("b3", "Due tomorrow", "to do", "open", at(16, time.October, 9), bob),
		task("b4", "Overdue", "to do", "open", at(12, time.October, 9), bob),
		task("a1", "Alice's task", "to do", "open", at(15, time.October, 9), alice),
		post,
	}}
	r, m := newRouter(cu)
	r.Uploads = "Uploads"
	r.Team = []Contact{{Name: "bob", Number: bobNumber}}
	r.Contacts = whatsapp.NewContacts()
	return r, m
}

func TestRemindSendsDirectlyWhenWindowIsOpen(t *testing.T) {
	r, m := remindRouter(t)
	r.Contacts.Record(bobNumber, now.Add(-20*time.Hour))

	reply := r.Reply(context.Background(), "/remind bob")
	if !strings.Contains(reply, "✅ Sent Bob a reminder: 2 tasks due today") {
		t.Fatalf("reply = %q", reply)
	}
	if len(m.sent) != 1 || m.sent[0].to != bobNumber {
		t.Fatalf("sent = %+v, want one message to Bob", m.sent)
	}
	body := m.sent[0].body
	for _, want := range []string{"Hi Bob", "Due today · Thu 15 Oct", "Fix checkout bug", "📤 Zaks: Diwali reel", "Reply *OK*"} {
		if !strings.Contains(body, want) {
			t.Errorf("message missing %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"Done already", "Due tomorrow", "Overdue", "Alice"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("message should not contain %q:\n%s", unwanted, body)
		}
	}
}

func TestRemindGivesLinkWhenWindowIsClosed(t *testing.T) {
	r, m := remindRouter(t)
	r.Contacts.Record(bobNumber, now.Add(-24*time.Hour+5*time.Minute)) // inside the safety margin

	reply := r.Reply(context.Background(), "/remind bob")
	if len(m.sent) != 0 {
		t.Fatalf("nothing should be sent when the window is closed, sent %+v", m.sent)
	}
	link := reply[strings.Index(reply, "https://wa.me/"):]
	link, _, _ = strings.Cut(link, "\n")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/"+bobNumber {
		t.Errorf("link goes to %q", u.Path)
	}
	if strings.Contains(u.RawQuery, "+") {
		t.Errorf("spaces must be %%20, not +: %s", u.RawQuery)
	}
	if text := u.Query().Get("text"); !strings.HasPrefix(text, "Hi Bob 👋") || !strings.Contains(text, "Fix checkout bug") {
		t.Errorf("prefilled text = %q", text)
	}
}

func TestRemindFallsBackToLinkWhenMetaRejects(t *testing.T) {
	r, _ := remindRouter(t)
	wm := &windowMessenger{closed: map[string]bool{bobNumber: true}}
	r.Messenger = wm
	r.Contacts.Record(bobNumber, now.Add(-time.Hour))

	reply := r.Reply(context.Background(), "/remind bob")
	if !strings.Contains(reply, "window has closed") || !strings.Contains(reply, "https://wa.me/"+bobNumber) {
		t.Fatalf("reply = %q", reply)
	}
	if len(wm.templates) != 0 {
		t.Fatal("/remind must never send a paid template")
	}
}

func TestRemindEdgeCases(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/remind", "Numbers saved for:*\n• bob"},
		{"/remind zed", "I don't recognise *zed*"},
		{"/remind alice", "matches 2 people"},
		{"/remind anna", "No WhatsApp number saved for Anna Lee"},
		{"/Reminder Anna", "No WhatsApp number saved for Anna Lee"},
		{"/reminder", "Due soon"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			r, m := remindRouter(t)
			if got := r.Reply(context.Background(), tt.in); !strings.Contains(got, tt.want) {
				t.Errorf("got %q, want it to contain %q", got, tt.want)
			}
			if len(m.sent) != 0 {
				t.Errorf("sent %+v", m.sent)
			}
		})
	}
}

func TestRemindNothingDue(t *testing.T) {
	r, m := remindRouter(t)
	r.Team = append(r.Team, Contact{Name: "anna", Number: "447700900789"})
	r.Contacts.Record("447700900789", now)
	if got := r.Reply(context.Background(), "/remind anna"); !strings.Contains(got, "Anna has nothing due today") {
		t.Fatalf("got %q", got)
	}
	if len(m.sent) != 0 {
		t.Fatalf("sent %+v", m.sent)
	}
}

func TestRemindCapsLongLists(t *testing.T) {
	var ts []clickup.Task
	for i := 0; i < remindMax+3; i++ {
		ts = append(ts, task(string(rune('a'+i)), "Task", "to do", "open", at(15, time.October, 9), bob))
	}
	msg := RemindMessage("Bob", ts, nil, now)
	if !strings.Contains(msg, "+3 more") {
		t.Fatalf("expected +3 more:\n%s", msg)
	}
}
