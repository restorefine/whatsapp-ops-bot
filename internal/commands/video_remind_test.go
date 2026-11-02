package commands

import (
	"context"
	"strings"
	"testing"
)

func TestVideoRemindersUseSeparateAdminOnlyPath(t *testing.T) {
	r, m := remindRouter(t)
	calls := 0
	r.VideoRemind = func(_ context.Context, name string) string {
		calls++
		if name != "Blanc Events" {
			t.Errorf("client=%q", name)
		}
		return "Video reminder queued"
	}
	if reply := r.Reply(context.Background(), "/remind videos Blanc Events"); reply != "Video reminder queued" {
		t.Fatal(reply)
	}
	if reply := r.Handle(context.Background(), Contact{Name: "bob", Number: bobNumber}, RoleTeam, "/remind videos Blanc Events"); reply != AdminOnlyReply {
		t.Fatal("team member could use video reminders")
	}
	if calls != 1 {
		t.Fatal("video reminder dispatch count", calls)
	}
	// Existing task reminders still provide a link outside the free window.
	if reply := r.Reply(context.Background(), "/remind bob"); !strings.Contains(reply, "https://wa.me/") {
		t.Fatal(reply)
	}
	if len(m.sent) != 0 {
		t.Fatal("task reminder sent a message outside the free window")
	}
}
