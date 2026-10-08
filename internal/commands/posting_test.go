package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/metricool"
)

type fakePosts struct {
	brands []metricool.Brand
	posts  map[int][]metricool.Post
}

func (f *fakePosts) Brands(context.Context) ([]metricool.Brand, error) { return f.brands, nil }

func (f *fakePosts) Posts(_ context.Context, id int, from, to time.Time) ([]metricool.Post, error) {
	var out []metricool.Post
	for _, p := range f.posts[id] {
		if !p.Time.Before(from) && p.Time.Before(to) {
			out = append(out, p)
		}
	}
	return out, nil
}

func published(at time.Time, nets ...string) metricool.Post {
	p := metricool.Post{Time: at}
	for _, n := range nets {
		p.Networks = append(p.Networks, metricool.Network{Name: n, Status: "PUBLISHED"})
	}
	return p
}

func uploadTask(id, list string, due *time.Time, status, typ string) clickup.Task {
	t := task(id, list+" post", status, typ, due)
	t.FolderName, t.ListName = "Uploads", list
	return t
}

// postingRouter: Masala posted, Zaks planned twice and posted once, ChocSpot
// mapped to Metricool's "ChocStop" and not posted, Mannis by hand and ticked,
// Indian at roundabout not in Metricool, RestoRefine posted unplanned.
func postingRouter(t *testing.T) *Router {
	t.Helper()
	today := at(15, time.October, 9)
	yesterday := at(14, time.October, 9)
	cu := &fakeClickUp{members: team, tasks: []clickup.Task{
		uploadTask("u1", "Masala", today, "to do", "open"),
		uploadTask("u2", "Zaks Drive thru (V)", today, "to do", "open"),
		uploadTask("u3", "Zaks Drive thru (V)", today, "to do", "open"),
		uploadTask("u4", "ChocSpot", today, "to do", "open"),
		uploadTask("u5", "Mannis", today, "complete", "done"),
		uploadTask("u6", "Indian at roundabout", today, "to do", "open"),
		uploadTask("u7", "Masala", yesterday, "to do", "open"),
		uploadTask("u8", "swagath", yesterday, "cancelled", "closed"),
	}}
	r, _ := newRouter(cu)
	r.Uploads = "Uploads"
	r.Metricool = &fakePosts{
		brands: []metricool.Brand{{ID: 1, Label: "Masala"}, {ID: 2, Label: "Zaks"}, {ID: 3, Label: "ChocStop"}, {ID: 4, Label: "RestoRefine"}, {ID: 5, Label: "swagath_barrhead"}},
		posts: map[int][]metricool.Post{
			1: {published(now.Add(-2*time.Hour), "facebook", "instagram", "tiktok")},
			2: {published(now.Add(-time.Hour), "instagram")},
			4: {published(now.Add(-3*time.Hour), "instagram")},
		},
	}
	r.BrandMap = BrandMap(map[string]string{"ChocSpot": "ChocStop"})
	r.Manual = []string{"Mannis"}
	return r
}

func TestPostedReport(t *testing.T) {
	r := postingRouter(t)
	got := r.Reply(context.Background(), "/posted")
	for _, want := range []string{
		"*❌ Not posted* (2)\n• ChocSpot\n• Zaks Drive thru (V) · 1 of 2 posted",
		"*✅ Posted* (1)\n• Masala · 12:00 PM · FB, IG, TikTok",
		"*✋ Posted by hand* (2)\n• Indian at roundabout · ❌ not ticked in ClickUp · _not in Metricool_\n• Mannis · ✅ ticked in ClickUp",
		"*➕ Also posted, not planned in ClickUp* (1)\n• RestoRefine · 11:00 AM · IG",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if got := r.Reply(context.Background(), "/posted yesterday"); !strings.Contains(got, "Posting check · Wed 14 Oct") || !strings.Contains(got, "• Masala") || strings.Contains(got, "Swagath") {
		t.Errorf("yesterday (cancelled upload must be left out):\n%s", got)
	}
}

func TestPostingMessages(t *testing.T) {
	r := postingRouter(t)
	r.Deadlines = Deadlines{ByBrand: map[string][2]int{brandKey("Masala"): {7, 0}}, Default: [2]int{17, 0}, HasDefault: true}
	ctx := context.Background()
	morning, err := r.PostingMessage(ctx, PostingEvent{Kind: PostingMorning})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"*📤 Planned* (5)", "• Masala · by 7:00 AM · ✅ already out", "• Zaks Drive thru (V) · by 5:00 PM · 2 posts", "• Mannis · by 5:00 PM · ✋ by hand · ✅ already out", "*⚠️ Didn't go out yesterday* (1)\n• Masala"} {
		if !strings.Contains(morning, want) {
			t.Errorf("morning missing %q:\n%s", want, morning)
		}
	}
	midday, _ := r.PostingMessage(ctx, PostingEvent{Kind: PostingMidday})
	if !strings.Contains(midday, "*❌ Not out yet* (3)") || !strings.Contains(midday, "• ChocSpot · by 5:00 PM") || !strings.Contains(midday, "*✅ Already out* (2)") {
		t.Errorf("midday:\n%s", midday)
	}

	early, _ := r.PostingMessage(ctx, PostingEvent{Kind: PostingCheck, Deadline: [2]int{7, 0}})
	if !strings.Contains(early, "Posting check · 7:00 AM deadline") || !strings.Contains(early, "• Masala") || strings.Contains(early, "Zaks") || strings.Contains(early, "RestoRefine") {
		t.Errorf("07:00 check should cover only Masala:\n%s", early)
	}
	late, _ := r.PostingMessage(ctx, PostingEvent{Kind: PostingCheck, Deadline: [2]int{17, 0}, Last: true})
	if strings.Contains(late, "Masala") || !strings.Contains(late, "Zaks Drive thru (V) · 1 of 2 posted") || !strings.Contains(late, "RestoRefine") {
		t.Errorf("17:00 check should cover the rest and the unplanned posts:\n%s", late)
	}

	// Nothing planned and nothing posted: no message at all.
	empty := postingRouter(t)
	empty.ClickUp = &fakeClickUp{members: team}
	empty.Metricool = &fakePosts{}
	empty.Deadlines = r.Deadlines
	for _, ev := range PostingSchedule([][2]int{{8, 0}, {12, 0}}, r.Deadlines) {
		if msg, _ := empty.PostingMessage(ctx, ev); msg != "" {
			t.Errorf("%+v with nothing to report sent %q", ev, msg)
		}
	}
}

func TestPostingSchedule(t *testing.T) {
	d := Deadlines{ByBrand: map[string][2]int{"failte": {7, 0}, "swagath": {17, 0}}, Default: [2]int{17, 0}, HasDefault: true}
	got := PostingSchedule([][2]int{{8, 0}, {12, 0}}, d)
	want := []PostingEvent{
		{At: [2]int{8, 0}, Kind: PostingMorning},
		{At: [2]int{12, 0}, Kind: PostingMidday},
		{At: [2]int{7, 15}, Kind: PostingCheck, Deadline: [2]int{7, 0}},
		{At: [2]int{17, 15}, Kind: PostingCheck, Deadline: [2]int{17, 0}, Last: true},
	}
	if len(got) != len(want) {
		t.Fatalf("schedule = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if at, _ := d.For("Failte"); at != [2]int{7, 0} {
		t.Errorf("Failte deadline = %v", at)
	}
	if at, _ := d.For("Zaks Drive thru"); at != [2]int{17, 0} {
		t.Errorf("default deadline = %v", at)
	}
}

func TestPostingReminderSendsToAdmins(t *testing.T) {
	r := postingRouter(t)
	r.Deadlines = Deadlines{Default: [2]int{17, 0}, HasDefault: true}
	m := r.Messenger.(*fakeMessenger)
	pr := &PostingReminder{Router: r, Admins: []string{"111", "222"}}
	pr.Send(context.Background(), PostingEvent{Kind: PostingCheck, Deadline: [2]int{17, 0}, Last: true})
	if len(m.sent) != 2 || m.sent[0].to != "111" || !strings.Contains(m.sent[1].body, "Posting check · 5:00 PM deadline") {
		t.Errorf("sent = %+v", m.sent)
	}
}

func TestMatchBrand(t *testing.T) {
	r := &Router{BrandMap: BrandMap(map[string]string{"d2dAlloway": "day.today.alloway"})}
	brands := []metricool.Brand{{ID: 1, Label: "Premier Ferguslie"}, {ID: 2, Label: "premier.speyavenue"}, {ID: 3, Label: "weeTutor"}, {ID: 4, Label: "blanceventsuk"}, {ID: 5, Label: "day.today.alloway"}, {ID: 6, Label: "himalayandinein"}}
	for list, want := range map[string]int{
		"Premier Ferguslie": 1, "Premier spey": 2, "WeTutor": 3, "blanc events": 4,
		"d2d Alloway": 5, "Himalayan dine in": 6, "FavSpot": 0, "Premier": 0, // ambiguous
	} {
		b, ok := r.matchBrand(list, brands)
		if (want == 0) == ok || ok && b.ID != want {
			t.Errorf("matchBrand(%q) = %v, %v; want brand %d", list, b, ok, want)
		}
	}
}

func TestParseDay(t *testing.T) {
	for in, want := range map[string]int{"": 15, "today": 15, "yesterday": 14, "thu": 15, "mon": 12, "Friday": 9} {
		d, ok := parseDay(in, now)
		if !ok || d.Day() != want {
			t.Errorf("parseDay(%q) = %v, %v; want day %d", in, d, ok, want)
		}
	}
	if _, ok := parseDay("someday", now); ok {
		t.Error("nonsense should not parse")
	}
}
