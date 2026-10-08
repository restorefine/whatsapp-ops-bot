package commands

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/metricool"
)

// Posts is what the posting checks need from Metricool. Tests use a fake.
type Posts interface {
	Brands(ctx context.Context) ([]metricool.Brand, error)
	Posts(ctx context.Context, brandID int, from, to time.Time) ([]metricool.Post, error)
}

// Brand is one client's uploads for a day: planned in ClickUp's Uploads
// folder and, unless posted by hand, published through Metricool.
type Brand struct {
	Name      string           // ClickUp list name, e.g. "Masala"
	Planned   int              // upload tasks due that day
	Ticked    int              // of those, marked complete in ClickUp
	Manual    bool             // posted by hand: the ClickUp tick is all we can check
	NotLinked bool             // no Metricool brand matches; treated as manual
	Posts     []metricool.Post // published that day
	Failed    bool             // Metricool reported an error on a post that day
}

// Posted is how many of the day's planned posts went out.
func (b Brand) Posted() int {
	if b.Manual || b.NotLinked {
		return b.Ticked
	}
	return len(b.Posts)
}

// Done reports whether every planned post went out.
func (b Brand) Done() bool { return b.Planned > 0 && b.Posted() >= b.Planned }

// PostingDay is the posting status of every client on one day.
type PostingDay struct {
	Day       time.Time // midnight, bot time zone
	Brands    []Brand   // clients with uploads planned that day
	Unplanned []Brand   // Metricool brands that posted with nothing planned in ClickUp
	NoMetric  bool      // Metricool isn't configured: everything uses the ClickUp tick
}

var parens = regexp.MustCompile(`\([^)]*\)`)

// BrandKey is brandKey for callers outside the package.
func BrandKey(s string) string { return brandKey(s) }

// brandKey is how ClickUp list names and Metricool labels are compared:
// "Zaks Drive thru (V)" and "zaks" both reduce to letters and digits.
func brandKey(s string) string { return squash(parens.ReplaceAllString(s, "")) }

// matchBrand finds the Metricool brand for a ClickUp list: the explicit
// mapping first, otherwise the one brand whose name contains the list's or
// is contained in it.
func (r *Router) matchBrand(list string, brands []metricool.Brand) (metricool.Brand, bool) {
	key := brandKey(list)
	if label, ok := r.BrandMap[key]; ok {
		for _, b := range brands {
			if brandKey(b.Label) == brandKey(label) {
				return b, true
			}
		}
		return metricool.Brand{}, false
	}
	var found []metricool.Brand
	for _, b := range brands {
		if bk := brandKey(b.Label); bk != "" && key != "" && (strings.Contains(bk, key) || strings.Contains(key, bk)) {
			found = append(found, b)
		}
	}
	if len(found) != 1 {
		return metricool.Brand{}, false
	}
	return found[0], true
}

// BrandMap keys a "ClickUp list → Metricool brand" mapping the way matchBrand
// looks it up.
func BrandMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for list, brand := range m {
		out[brandKey(list)] = brand
	}
	return out
}

func (r *Router) isManual(list string) bool {
	for _, m := range r.Manual {
		if brandKey(m) == brandKey(list) {
			return true
		}
	}
	return false
}

// postingDay works out what was planned and what went out on day.
func (r *Router) postingDay(ctx context.Context, day time.Time) (PostingDay, error) {
	pd := PostingDay{Day: day, NoMetric: r.Metricool == nil}
	tasks, err := r.ClickUp.Tasks(ctx, clickup.TaskFilter{DueAfter: day.Add(-time.Millisecond), DueBefore: day.AddDate(0, 0, 1), IncludeClosed: true})
	if err != nil {
		return pd, err
	}
	_, uploads := r.splitUploads(tasks)
	byList := map[string]*Brand{}
	var order []string
	for _, t := range uploads {
		if t.DueDate == nil || !dateOf(*t.DueDate, r.Loc).Equal(day) || strings.Contains(strings.ToLower(t.Status), "cancel") {
			continue
		}
		name := strings.TrimSpace(t.ListName)
		key := brandKey(name)
		b, ok := byList[key]
		if !ok {
			b = &Brand{Name: displayName(name), Manual: r.isManual(name)}
			byList[key] = b
			order = append(order, key)
		}
		b.Planned++
		if t.IsDone() {
			b.Ticked++
		}
	}

	var brands []metricool.Brand
	if r.Metricool != nil {
		if brands, err = r.Metricool.Brands(ctx); err != nil {
			return pd, fmt.Errorf("metricool: %w", err)
		}
	}
	linked := map[int]bool{}
	for _, key := range order {
		b := byList[key]
		if b.Manual {
			continue
		}
		mb, ok := r.matchBrand(b.Name, brands)
		if !ok {
			b.NotLinked = true
			continue
		}
		linked[mb.ID] = true
		if b.Posts, b.Failed, err = r.published(ctx, mb.ID, day); err != nil {
			return pd, err
		}
	}
	for _, key := range order {
		pd.Brands = append(pd.Brands, *byList[key])
	}
	sort.Slice(pd.Brands, func(i, j int) bool { return strings.ToLower(pd.Brands[i].Name) < strings.ToLower(pd.Brands[j].Name) })

	// Posts on brands with nothing planned in ClickUp that day.
	for _, mb := range brands {
		if linked[mb.ID] || r.isManual(mb.Label) {
			continue
		}
		posts, failed, err := r.published(ctx, mb.ID, day)
		if err != nil {
			return pd, err
		}
		if len(posts) > 0 || failed {
			pd.Unplanned = append(pd.Unplanned, Brand{Name: displayName(mb.Label), Posts: posts, Failed: failed})
		}
	}
	return pd, nil
}

// displayName capitalises names typed in lower case ("blanc events"), and
// leaves deliberate ones ("d2d Alloway", "weeTutor") alone.
func displayName(s string) string {
	if s == strings.ToLower(s) {
		return titleCase(s)
	}
	return s
}

// published returns a brand's published posts on day, earliest first, and
// whether any post that day failed.
func (r *Router) published(ctx context.Context, brandID int, day time.Time) ([]metricool.Post, bool, error) {
	posts, err := r.Metricool.Posts(ctx, brandID, day, day.AddDate(0, 0, 1))
	if err != nil {
		return nil, false, fmt.Errorf("metricool: %w", err)
	}
	var out []metricool.Post
	failed := false
	for _, p := range posts {
		if p.Draft {
			continue
		}
		if p.Published() {
			out = append(out, p)
		}
		failed = failed || p.Failed()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, failed, nil
}

// --- /posted ---

func (r *Router) posted(ctx context.Context, args string) string {
	now := r.now()
	day, ok := parseDay(args, now)
	if !ok {
		return "Send */posted* for today, or */posted yesterday*, */posted mon* and so on."
	}
	pd, err := r.postingDay(ctx, day)
	if err != nil {
		return r.postingFailed(err)
	}
	return PostedReport(pd, now)
}

func (r *Router) postingFailed(err error) string {
	r.Log.Error("posting check failed", "err", err)
	return "Couldn't check posts right now. Try again in a minute.\n_" + err.Error() + "_"
}

// parseDay reads "", "today", "yesterday" or a weekday ("mon", "monday"),
// meaning the most recent one up to today.
func parseDay(s string, now time.Time) (time.Time, bool) {
	today := dateOf(now, now.Location())
	switch s = strings.ToLower(strings.TrimSpace(s)); s {
	case "", "today":
		return today, true
	case "yesterday":
		return today.AddDate(0, 0, -1), true
	}
	for back := 0; back < 7; back++ {
		d := today.AddDate(0, 0, -back)
		name := strings.ToLower(d.Weekday().String())
		if len(s) >= 3 && strings.HasPrefix(name, s) {
			return d, true
		}
	}
	return time.Time{}, false
}

// PostedReport is the full status for a day: not posted, posted, manual and
// posts nobody planned. now must be in the bot's time zone.
func PostedReport(pd PostingDay, now time.Time) string {
	title := "📤 Posting check"
	if !pd.Day.Equal(dateOf(now, now.Location())) {
		title += " · " + shortDate(pd.Day, now)
	}
	return postedReport(title, pd, now)
}

func postedReport(title string, pd PostingDay, now time.Time) string {
	var b strings.Builder
	header(&b, title, now)
	if len(pd.Brands) == 0 && len(pd.Unplanned) == 0 {
		b.WriteString("\nNo uploads planned in ClickUp for this day.")
		return b.String()
	}
	var missing, done, manual []Brand
	for _, br := range pd.Brands {
		switch {
		case br.Manual || br.NotLinked:
			manual = append(manual, br)
		case br.Done():
			done = append(done, br)
		default:
			missing = append(missing, br)
		}
	}
	if len(missing) > 0 {
		heading(&b, "❌ Not posted", len(missing))
		for _, br := range missing {
			b.WriteString("• " + br.Name + progress(br) + "\n")
		}
	}
	if len(done) > 0 {
		heading(&b, "✅ Posted", len(done))
		for _, br := range done {
			b.WriteString("• " + br.Name + " · " + postTimes(br.Posts) + "\n")
		}
	}
	if len(manual) > 0 {
		heading(&b, "✋ Posted by hand", len(manual))
		for _, br := range manual {
			mark := "❌ not ticked in ClickUp"
			if br.Done() {
				mark = "✅ ticked in ClickUp"
			} else if br.Ticked > 0 {
				mark = fmt.Sprintf("⚠️ %d of %d ticked in ClickUp", br.Ticked, br.Planned)
			}
			note := ""
			if br.NotLinked && !pd.NoMetric {
				note = " · _not in Metricool_"
			}
			b.WriteString("• " + br.Name + " · " + mark + note + "\n")
		}
	}
	if len(pd.Unplanned) > 0 {
		heading(&b, "➕ Also posted, not planned in ClickUp", len(pd.Unplanned))
		for _, br := range pd.Unplanned {
			b.WriteString("• " + br.Name + " · " + postTimes(br.Posts) + failedNote(br) + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// progress is " · 1 of 2 posted" or " · ⚠️ failed in Metricool" for a client
// that isn't done.
func progress(br Brand) string {
	s := ""
	if br.Planned > 1 || br.Posted() > 0 {
		s = fmt.Sprintf(" · %d of %d posted", br.Posted(), br.Planned)
	}
	return s + failedNote(br)
}

func failedNote(br Brand) string {
	if br.Failed {
		return " · ⚠️ _failed in Metricool_"
	}
	return ""
}

// postTimes is "19:29 UK · FB, IG, TikTok", one entry per post.
func postTimes(posts []metricool.Post) string {
	var parts []string
	for _, p := range posts {
		at := p.Time.Format("15:04")
		if clock.Label != "" {
			at += " " + clock.Label
		}
		var nets []string
		for _, n := range p.Networks {
			if n.Status == "PUBLISHED" {
				nets = append(nets, networkName(n.Name))
			}
		}
		parts = append(parts, at+" · "+strings.Join(nets, ", "))
	}
	return strings.Join(parts, "; ")
}

func networkName(n string) string {
	switch n {
	case "facebook":
		return "FB"
	case "instagram":
		return "IG"
	case "tiktok":
		return "TikTok"
	}
	return titleCase(n)
}

// --- automatic posting messages ---

// checkDelay is how long after a deadline the bot checks whether posts went out.
const checkDelay = 15 * time.Minute

// Deadlines says by what time each client's posts must be out, in the bot's
// time zone.
type Deadlines struct {
	ByBrand    map[string][2]int // brandKey(ClickUp list) → hour, minute
	Default    [2]int            // every other client
	HasDefault bool
}

// For returns a client's deadline.
func (d Deadlines) For(list string) ([2]int, bool) {
	key := brandKey(list)
	for k, at := range d.ByBrand {
		if k != "" && (k == key || strings.Contains(key, k)) {
			return at, true
		}
	}
	return d.Default, d.HasDefault
}

// PostingKind is which of the day's automatic messages to send.
type PostingKind int

const (
	PostingMorning PostingKind = iota // what's planned today, and what didn't go out yesterday
	PostingMidday                     // what's still to go out
	PostingCheck                      // whether the clients with one deadline posted
)

// PostingEvent is one automatic message: when, what, and for a check, which
// deadline it covers.
type PostingEvent struct {
	At       [2]int
	Kind     PostingKind
	Deadline [2]int // PostingCheck only
	Last     bool   // the day's last check, which also lists posts nobody planned
}

// PostingSchedule turns the summary times and the deadlines into the day's
// events: the first summary is the morning one, the others midday ones, and
// each distinct deadline gets a check 15 minutes after it.
func PostingSchedule(summaries [][2]int, d Deadlines) []PostingEvent {
	var events []PostingEvent
	for i, at := range summaries {
		kind := PostingMidday
		if i == 0 {
			kind = PostingMorning
		}
		events = append(events, PostingEvent{At: at, Kind: kind})
	}
	seen := map[[2]int]bool{}
	var deadlines [][2]int
	for _, at := range d.ByBrand {
		if !seen[at] {
			seen[at] = true
			deadlines = append(deadlines, at)
		}
	}
	if d.HasDefault && !seen[d.Default] {
		deadlines = append(deadlines, d.Default)
	}
	sort.Slice(deadlines, func(i, j int) bool { return minutes(deadlines[i]) < minutes(deadlines[j]) })
	for i, dl := range deadlines {
		m := minutes(dl) + int(checkDelay/time.Minute)
		events = append(events, PostingEvent{At: [2]int{m / 60 % 24, m % 60}, Kind: PostingCheck, Deadline: dl, Last: i == len(deadlines)-1})
	}
	return events
}

func minutes(t [2]int) int { return t[0]*60 + t[1] }

func hhmm(t [2]int) string { return fmt.Sprintf("%02d:%02d", t[0], t[1]) }

// byWhen is " · by 17:00" for a client with a deadline.
func (r *Router) byWhen(list string) string {
	if at, ok := r.Deadlines.For(list); ok {
		return " · by " + hhmm(at)
	}
	return ""
}

// PostingMessage builds an automatic message, or "" when there is nothing
// worth sending.
func (r *Router) PostingMessage(ctx context.Context, ev PostingEvent) (string, error) {
	now := r.now()
	today := dateOf(now, r.Loc)
	pd, err := r.postingDay(ctx, today)
	if err != nil {
		return "", err
	}
	switch ev.Kind {
	case PostingMorning:
		yesterday, err := r.postingDay(ctx, today.AddDate(0, 0, -1))
		if err != nil {
			return "", err
		}
		return r.morningReport(pd, yesterday, now), nil
	case PostingMidday:
		return r.middayReport(pd, now), nil
	}
	var due []Brand
	for _, br := range pd.Brands {
		if at, ok := r.Deadlines.For(br.Name); ok && at == ev.Deadline {
			due = append(due, br)
		}
	}
	pd.Brands = due
	if !ev.Last {
		pd.Unplanned = nil
	}
	if len(pd.Brands) == 0 && len(pd.Unplanned) == 0 {
		return "", nil
	}
	return postedReport("📤 Posting check · "+hhmm(ev.Deadline)+" deadline", pd, now), nil
}

// morningReport lists today's planned uploads with their deadlines, and
// yesterday's misses.
func (r *Router) morningReport(today, yesterday PostingDay, now time.Time) string {
	var missed []Brand
	for _, br := range yesterday.Brands {
		if !br.Done() {
			missed = append(missed, br)
		}
	}
	if len(today.Brands) == 0 && len(missed) == 0 {
		return ""
	}
	var b strings.Builder
	header(&b, "☀️ Posting today", now)
	if len(today.Brands) == 0 {
		b.WriteString("\nNo uploads planned in ClickUp today.\n")
	} else {
		heading(&b, "📤 Planned", len(today.Brands))
		for _, br := range today.Brands {
			line := "• " + br.Name + r.byWhen(br.Name)
			if br.Planned > 1 {
				line += fmt.Sprintf(" · %d posts", br.Planned)
			}
			if br.Manual || br.NotLinked {
				line += " · ✋ by hand"
			}
			if br.Done() {
				line += " · ✅ already out"
			}
			b.WriteString(line + "\n")
		}
	}
	if len(missed) > 0 {
		heading(&b, "⚠️ Didn't go out yesterday", len(missed))
		for _, br := range missed {
			b.WriteString("• " + br.Name + progress(br) + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// middayReport lists what's still to go out today.
func (r *Router) middayReport(pd PostingDay, now time.Time) string {
	var left, out []Brand
	for _, br := range pd.Brands {
		if br.Done() {
			out = append(out, br)
		} else {
			left = append(left, br)
		}
	}
	if len(left) == 0 {
		return ""
	}
	var b strings.Builder
	header(&b, "⏰ Still to post today", now)
	heading(&b, "❌ Not out yet", len(left))
	for _, br := range left {
		line := "• " + br.Name + r.byWhen(br.Name) + progress(br)
		if br.Manual || br.NotLinked {
			line += " · ✋ by hand"
		}
		b.WriteString(line + "\n")
	}
	if len(out) > 0 {
		heading(&b, "✅ Already out", len(out))
		for _, br := range out {
			b.WriteString("• " + br.Name + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// PostingReminder sends the posting messages to the admins every day. It is
// the only thing the bot does without being asked.
type PostingReminder struct {
	Router *Router
	Admins []string
	Events []PostingEvent
}

// Run sends each event at its time until ctx is cancelled.
func (m *PostingReminder) Run(ctx context.Context) {
	log := m.Router.Log
	for {
		now := m.Router.now()
		next, idx := time.Time{}, 0
		for i, ev := range m.Events {
			if at := NextRun(now, ev.At[0], ev.At[1]); next.IsZero() || at.Before(next) {
				next, idx = at, i
			}
		}
		log.Info("posting message scheduled", "at", next.Format(time.RFC3339), "kind", m.Events[idx].Kind)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		sendCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		m.Send(sendCtx, m.Events[idx])
		cancel()
	}
}

// Send builds one message and delivers it to every admin. A closed 24 hour
// window just means WhatsApp drops the message, at no cost.
func (m *PostingReminder) Send(ctx context.Context, ev PostingEvent) {
	r := m.Router
	msg, err := r.PostingMessage(ctx, ev)
	if err != nil {
		r.Log.Error("posting message failed", "kind", ev.Kind, "err", err)
		return
	}
	if msg == "" {
		r.Log.Info("posting message: nothing to report", "kind", ev.Kind)
		return
	}
	for _, to := range m.Admins {
		if err := r.Messenger.SendText(ctx, to, msg); err != nil {
			r.Log.Error("posting message: send failed", "to_suffix", suffix(to), "err", err)
			continue
		}
		r.Log.Info("posting message sent", "kind", ev.Kind, "to_suffix", suffix(to))
	}
}
