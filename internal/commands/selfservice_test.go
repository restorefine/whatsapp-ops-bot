package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

const (
	bobPhone   = "9779860906634"
	adminPhone = "447590990552"
	pmPhone    = "9779812378182"
)

// selfRouter has Bob on the team, Anna as the PM (an admin with tasks) and a
// founder with no ClickUp name.
func selfRouter(t *testing.T) (*Router, *fakeMessenger, *fakeClickUp) {
	t.Helper()
	weeOld := task("w1", "WeeTutor", "to do", "open", at(14, time.October, 9), bob)
	weeOld.ListID = "L1"
	weeToday := task("w2", "WeeTutor", "to do", "open", at(15, time.October, 9), bob)
	weeToday.ListID, weeToday.HasBrief = "L1", true
	graphic := task("w3", "WeeTutor Graphic", "in progress", "custom", at(17, time.October, 9), bob)
	graphic.ListID = "L1"
	masala := task("m1", "Masala", "to do", "open", at(15, time.October, 9), bob)
	masala.ListID = "L1"
	cu := &fakeClickUp{
		members: team,
		me:      clickup.Member{ID: 99, Username: "Harpreet Singh"},
		tasks: []clickup.Task{weeOld, weeToday, graphic, masala,
			task("a1", "Alice's task", "to do", "open", at(15, time.October, 9), alice),
			task("d1", "Done already", "complete", "done", at(15, time.October, 9), bob),
		},
	}
	r, m := newRouter(cu)
	r.Admins = []Contact{{Number: adminPhone}, {Name: "anna", Number: pmPhone}}
	r.Team = []Contact{{Name: "bob", Number: bobPhone}}
	r.Contacts = whatsapp.NewContacts()
	return r, m, cu
}

func say(r *Router, from, text string) string {
	p, role := r.WhoIs(from)
	return r.Handle(context.Background(), p, role, text)
}

func status(cu *fakeClickUp, id string) string {
	for _, t := range cu.tasks {
		if t.ID == id {
			return t.Status
		}
	}
	return ""
}

func TestTeamAccess(t *testing.T) {
	r, _, _ := selfRouter(t)
	for in, want := range map[string]string{
		"/update":         AdminOnlyReply,
		"/sagun":          AdminOnlyReply,
		"help":            TeamHelpText,
		"hello, all good": "",
		"2":               "", // no list yet: ordinary chat, stay quiet
	} {
		if got := say(r, bobPhone, in); got != want {
			t.Errorf("team %q = %q, want %q", in, got, want)
		}
	}
	if got := say(r, adminPhone, "help"); got != HelpText {
		t.Errorf("admin help = %q", got)
	}
	if _, role := r.WhoIs("15550001111"); role != RoleNone {
		t.Error("unknown numbers must have no role")
	}
}

func TestMyTasks(t *testing.T) {
	r, _, _ := selfRouter(t)
	got := say(r, bobPhone, "My Tasks")
	for _, want := range []string{"*⚠️ Overdue*\n*1* WeeTutor · _due Wed 14 Oct · 1 day late_", "*📌 Due today*\n*2* Masala", "*3* WeeTutor · _due today_ 💬", "*🔜 Upcoming*\n*4* WeeTutor Graphic", "*done 2, 3*"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"Alice's task", "Done already"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("my tasks should not show %q:\n%s", unwanted, got)
		}
	}
	if got := say(r, adminPhone, "my tasks"); !strings.Contains(got, "no ClickUp name saved") {
		t.Errorf("admin without a ClickUp name: %q", got)
	}
}

func TestTaskDetails(t *testing.T) {
	r, _, cu := selfRouter(t)
	cu.details = map[string]clickup.TaskDetail{"w2": {
		Description: "[\n\ndrive.google.com\n\nhttps://drive.google.com/file/d/abc/view?usp=drive\\_link\n\n](https://drive.google.com/file/d/abc/view?usp=drive_link)\n\n### Slide 1\n**Diwali offer**",
		Comments: []clickup.Comment{
			{Author: anna, Text: "Caption: Light up your Diwali", Date: now.Add(-time.Hour)},
			{Author: bob, Text: "on it", Date: now},
			{Author: clickup.Member{ID: 99}, Text: "✅ Marked complete by Bob via WhatsApp", Date: now},
		},
	}}
	say(r, bobPhone, "my tasks")
	got := say(r, bobPhone, "3")
	for _, want := range []string{"*📌 WeeTutor*", "*Brief*\nhttps://drive.google.com/file/d/abc/view?usp=drive_link\n\n*Slide 1*\n*Diwali offer*", "_Anna · Thu 15 Oct · 13:00_\nCaption: Light up your Diwali", "*done 3*"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"on it", "Marked complete"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("details should only show admins' comments, found %q:\n%s", unwanted, got)
		}
	}
	if got := say(r, bobPhone, "2"); !strings.Contains(got, "No brief or assets") {
		t.Errorf("task without a brief:\n%s", got)
	}
	if got := say(r, bobPhone, "9"); !strings.Contains(got, "no task 9") {
		t.Errorf("out of range: %q", got)
	}
}

func TestDoneSingleMatch(t *testing.T) {
	r, m, cu := selfRouter(t)
	r.Contacts.Record(pmPhone, now.Add(-time.Hour)) // PM's window is open, the founder's isn't

	got := say(r, bobPhone, "masala done")
	if !strings.Contains(got, "✅ Marked *Masala* (due today) complete.") || !strings.Contains(got, "undo") {
		t.Fatalf("reply = %q", got)
	}
	if status(cu, "m1") != "complete" {
		t.Error("task was not completed")
	}
	if c := cu.comments["m1"]; len(c) != 1 || !strings.HasPrefix(c[0], "✅ Marked complete by Bob via WhatsApp") || strings.Contains(c[0], "behalf") {
		t.Errorf("comment = %q", c)
	}
	if len(m.sent) != 1 || m.sent[0].to != pmPhone || !strings.Contains(m.sent[0].body, "*Bob* completed:\n• Masala") {
		t.Errorf("admin alerts = %+v, want one to the PM only", m.sent)
	}
}

func TestDoneAsksWhichOne(t *testing.T) {
	r, _, cu := selfRouter(t)
	got := say(r, bobPhone, "done wetutor") // misspelt on purpose
	if !strings.Contains(got, "You have 3 open tasks matching *wetutor*") || !strings.Contains(got, "*1* WeeTutor · _due Wed 14 Oct · 1 day late_") {
		t.Fatalf("reply = %q", got)
	}
	for _, id := range []string{"w1", "w2", "w3"} {
		if status(cu, id) == "complete" {
			t.Fatalf("%s completed before choosing", id)
		}
	}

	got = say(r, bobPhone, "1, 3, 7")
	if !strings.Contains(got, "Marked 2 tasks complete") || !strings.Contains(got, "Skipped 7") {
		t.Fatalf("reply = %q", got)
	}
	if status(cu, "w1") != "complete" || status(cu, "w3") != "complete" || status(cu, "w2") == "complete" {
		t.Errorf("statuses = %s %s %s", status(cu, "w1"), status(cu, "w2"), status(cu, "w3"))
	}

	// The question is answered: a later "2" doesn't complete anything.
	if got := say(r, bobPhone, "2"); got != "" {
		t.Errorf("stale number = %q", got)
	}
}

func TestDoneNumbersFromList(t *testing.T) {
	r, _, cu := selfRouter(t)
	say(r, bobPhone, "my tasks")
	if got := say(r, bobPhone, "done 2,3"); !strings.Contains(got, "Marked 2 tasks complete") {
		t.Fatalf("reply = %q", got)
	}
	if status(cu, "m1") != "complete" || status(cu, "w2") != "complete" {
		t.Error("numbers from my tasks were not completed")
	}
	if got := say(r, bobPhone, "1, 2"); !strings.Contains(got, "send *done 1, 2*") {
		t.Errorf("several bare numbers should explain, got %q", got)
	}
}

func TestDoneExpires(t *testing.T) {
	r, _, cu := selfRouter(t)
	say(r, bobPhone, "done weetutor")
	r.Now = func() time.Time { return now.Add(pendingTTL + time.Minute) }
	if got := say(r, bobPhone, "1"); got != "" {
		t.Errorf("expired choice = %q", got)
	}
	if status(cu, "w1") == "complete" {
		t.Error("an expired choice completed a task")
	}
}

func TestUndo(t *testing.T) {
	r, _, cu := selfRouter(t)
	say(r, bobPhone, "masala done")
	if got := say(r, bobPhone, "undo"); got != "↩️ Reopened Masala." {
		t.Fatalf("undo = %q", got)
	}
	if status(cu, "m1") != "to do" {
		t.Errorf("status after undo = %q", status(cu, "m1"))
	}
	if got := say(r, bobPhone, "undo"); !strings.Contains(got, "Nothing to undo") {
		t.Errorf("second undo = %q", got)
	}
}

func TestDoneNeverCancels(t *testing.T) {
	r, _, cu := selfRouter(t)
	cu.statuses = []clickup.Status{{Name: "to do", Type: "open"}, {Name: "cancelled", Type: "closed"}}
	if got := say(r, bobPhone, "masala done"); !strings.Contains(got, "Couldn't update Masala") {
		t.Fatalf("reply = %q", got)
	}
	if status(cu, "m1") != "to do" {
		t.Error("task was moved to a cancelled status")
	}
}

func TestAdminDoneOnBehalf(t *testing.T) {
	r, _, cu := selfRouter(t)
	if got := say(r, adminPhone, "done masala for bob"); !strings.Contains(got, "Marked *Masala*") {
		t.Fatalf("reply = %q", got)
	}
	if c := cu.comments["m1"]; len(c) != 1 || !strings.Contains(c[0], "by an admin on behalf of Bob") {
		t.Errorf("comment = %q", c)
	}
	if got := say(r, pmPhone, "weetutor graphic bob done"); !strings.Contains(got, "Marked *WeeTutor Graphic*") {
		t.Errorf("trailing name form = %q", got)
	}
	if c := cu.comments["w3"]; len(c) != 1 || !strings.Contains(c[0], "by Anna on behalf of Bob") {
		t.Errorf("comment = %q", c)
	}
	if got := say(r, bobPhone, "done alice's task for alice"); !strings.Contains(got, "can't find") {
		t.Errorf("a team member must not complete someone else's task, got %q", got)
	}
	if got := say(r, adminPhone, "done masala"); !strings.Contains(got, "Whose task") {
		t.Errorf("admin with no ClickUp name and no owner given: %q", got)
	}
}

func TestWhatsappText(t *testing.T) {
	for in, want := range map[string]string{
		"[\n\ndrive.google.com\n\nhttps://drive.google.com/x?usp=drive\\_link\n\n](https://drive.google.com/x?usp=drive_link)\n\nPizza": "https://drive.google.com/x?usp=drive_link\n\nPizza",
		"[Brand guide](https://example.com/g)":                  "Brand guide: https://example.com/g",
		"### Slide 2\n**Romantic**\nSoft, elegant.\n\n\n\nEnd":  "*Slide 2*\n*Romantic*\nSoft, elegant.\n\nEnd",
		"Food name: Sandwich (Eggs, Bacon and squared sausage)": "Food name: Sandwich (Eggs, Bacon and squared sausage)",
	} {
		if got := whatsappText(in); got != want {
			t.Errorf("whatsappText(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestClockLabels(t *testing.T) {
	london, _ := time.LoadLocation("Europe/London")
	SetClock(Clock{Label: "UK", Second: ktm, SecondLabel: "Nepal"})
	defer SetClock(Clock{})
	at := time.Date(2026, 10, 8, 9, 24, 0, 0, london)
	if got := stamp(at); got != "09:24 UK · 14:09 Nepal" {
		t.Errorf("stamp = %q", got)
	}
	due := time.Date(2026, 10, 8, 18, 0, 0, 0, london)
	timed := clickup.Task{DueDate: &due, DueHasTime: true}
	if got := dueText(timed, at); got != "Due today, 18:00 UK (22:45 Nepal)" {
		t.Errorf("timed due = %q", got)
	}
	timed.DueHasTime = false
	if got := dueText(timed, at); got != "Due today" {
		t.Errorf("date-only due = %q", got)
	}
}
