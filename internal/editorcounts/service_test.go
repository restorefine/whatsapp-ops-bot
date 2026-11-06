package editorcounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
)

type memoryStore struct {
	mu     sync.Mutex
	ledger Ledger
	fail   bool
}

func (m *memoryStore) UpdateEditorLedger(_ context.Context, f func(*Ledger) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, _ := json.Marshal(m.ledger)
	var working Ledger
	_ = json.Unmarshal(data, &working)
	if err := f(&working); err != nil {
		return err
	}
	if m.fail {
		return errors.New("Drive save failed")
	}
	m.ledger = working
	return nil
}

type source struct {
	tasks   []clickup.Task
	members []clickup.Member
	err     error
}

func (s *source) Tasks(_ context.Context, f clickup.TaskFilter) ([]clickup.Task, error) {
	if !f.IncludeClosed || !f.UpdatedAfter.IsZero() {
		panic("monthly or incomplete count")
	}
	return append([]clickup.Task{}, s.tasks...), s.err
}
func (s *source) Members(context.Context) ([]clickup.Member, error) { return s.members, s.err }

func fixture() (*Service, *source, *memoryStore, *time.Time) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	cu := &source{members: []clickup.Member{{ID: 1, Username: "Arpan Wasti"}, {ID: 2, Username: "Sunil Paudel"}, {ID: 3, Username: "Himal Joshi"}}}
	store := &memoryStore{}
	s := &Service{Source: cu, Store: store, Editors: []Editor{{Key: "arpan", Name: "Arpan"}, {Key: "sunil", Name: "Sunil"}, {Key: "himal", Name: "Himal"}}, Admins: []string{"111111111", "222222222", "333333333"}, Now: func() time.Time { return now }}
	return s, cu, store, &now
}
func completed(id string, member int, list string, when time.Time) clickup.Task {
	return clickup.Task{ID: id, Name: "Video " + id, ListName: list, Status: "complete", StatusType: "closed", Assignees: []clickup.Member{{ID: member}}, DateClosed: &when}
}

func TestFreshCyclesHistoryRollingAndRestart(t *testing.T) {
	s, cu, store, now := fixture()
	ctx := context.Background()
	cu.tasks = []clickup.Task{completed("old", 1, "Arpan", now.AddDate(0, -2, 0))}
	r, err := s.Report(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Editors[0].Total != 0 || r.Editors[0].HistoricalTotal != 1 || len(store.ledger.Alerts) != 0 {
		t.Fatal("old work entered new cycles")
	}
	*now = now.Add(40 * 24 * time.Hour)
	for i := 0; i < 45; i++ {
		cu.tasks = append(cu.tasks, completed(fmt.Sprintf("new%02d", i), 1, "Arpan", now.Add(time.Duration(i-45)*time.Hour)))
	}
	cu.tasks = append(cu.tasks, cu.tasks[1]) // duplicate across API pages
	r, err = s.Report(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := r.Editors[0]
	if e.Total != 45 || e.FullBatches != 2 || e.CurrentBatch != 3 || e.CurrentCount != 5 || e.Remaining != 15 || len(store.ledger.Alerts) != 6 {
		t.Fatalf("wrong cycle: %+v, alerts=%d", e, len(store.ledger.Alerts))
	}
	s2 := &Service{Source: cu, Store: store, Editors: s.Editors, Admins: s.Admins, Now: s.Now}
	r, err = s2.Report(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Editors[0].Total != 45 || len(store.ledger.Alerts) != 6 {
		t.Fatal("restart recounted tasks or requeued alerts")
	}
	for _, r := range r.Editors[0].Records {
		if !r.Imported && (r.Batch < 1 || r.Batch > 3 || r.Position < 1 || r.Position > 20) {
			t.Fatal("invalid batch evidence")
		}
	}
}

func TestExcludedTasksAndReopenKeepAudit(t *testing.T) {
	s, cu, store, now := fixture()
	ctx := context.Background()
	_, _ = s.Report(ctx)
	*now = now.Add(2 * time.Minute)
	valid := completed("valid", 1, "Arpan", now.Add(-time.Minute))
	hold := completed("hold", 1, "Arpan", *now)
	hold.Status = "hold"
	hold.StatusType = "done"
	cancelled := completed("cancel", 1, "Arpan", *now)
	cancelled.Status = "cancelled"
	shared := completed("shared", 1, "Arpan", *now)
	shared.Assignees = append(shared.Assignees, clickup.Member{ID: 2})
	missing := completed("missing", 1, "Arpan", *now)
	missing.DateClosed = nil
	missing.DateUpdated = *now
	sub := completed("sub", 1, "Arpan", *now)
	sub.ParentID = "parent"
	wrongList := completed("nonvideo", 1, "Website", *now)
	cu.tasks = []clickup.Task{valid, hold, cancelled, shared, missing, sub, wrongList}
	r, err := s.Report(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Editors[0].Total != 1 || len(r.Editors[0].Warnings) != 2 {
		t.Fatalf("bad filtering: %+v", r.Editors[0])
	}
	*now = now.Add(2 * time.Minute)
	cu.tasks[0].Status = "in progress"
	cu.tasks[0].StatusType = "custom"
	cu.tasks[0].DateClosed = nil
	r, err = s.Report(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Editors[0].Total != 1 || r.Editors[0].ReviewCount != 1 || store.ledger.Editors["arpan"][0].Title != "Video valid" {
		t.Fatal("reopen erased audit record")
	}
	*now = now.Add(2 * time.Minute)
	cu.tasks[0] = valid
	r, err = s.Report(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Editors[0].Total != 1 || r.Editors[0].ReviewCount != 0 {
		t.Fatal("recompletion double counted")
	}
}

func TestFailedSaveAndIncompleteFetchDoNotAdvance(t *testing.T) {
	s, cu, store, now := fixture()
	ctx := context.Background()
	_, _ = s.Report(ctx)
	*now = now.Add(2 * time.Minute)
	cu.tasks = []clickup.Task{completed("new", 1, "Arpan", *now)}
	store.fail = true
	if _, err := s.Report(ctx); err == nil {
		t.Fatal("failed write accepted")
	}
	if len(store.ledger.Editors["arpan"]) != 0 {
		t.Fatal("failed write changed audit")
	}
	store.fail = false
	cu.err = errors.New("page limit")
	if _, err := s.Report(ctx); err == nil {
		t.Fatal("partial fetch accepted")
	}
	cu.err = nil
	r, err := s.Report(ctx)
	if err != nil || r.Editors[0].Total != 1 {
		t.Fatal("retry did not recover", err)
	}
}

func TestAddingAndRemovingEditorRetainsPreviousHistory(t *testing.T) {
	s, cu, _, now := fixture()
	ctx := context.Background()
	_, _ = s.Report(ctx)
	*now = now.Add(2 * time.Minute)
	cu.tasks = []clickup.Task{completed("arpan-new", 1, "Arpan", *now)}
	_, _ = s.Report(ctx)
	s.Editors = s.Editors[1:]
	*now = now.Add(2 * time.Minute)
	r, err := s.Report(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Editors) != 3 || !r.Editors[2].Inactive || r.Editors[2].Total != 1 {
		t.Fatal("removed editor audit lost")
	}
	cu.members = append(cu.members, clickup.Member{ID: 4, Username: "Neweditor Name"})
	s.Editors = append(s.Editors, Editor{Key: "neweditor", Name: "Neweditor"})
	*now = now.Add(2 * time.Minute)
	cu.tasks = append(cu.tasks, completed("older-neweditor", 4, "Neweditor", now.AddDate(0, -1, 0)))
	r, err = s.Report(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Editors {
		if e.Key == "neweditor" && (e.Total != 0 || e.HistoricalTotal != 1) {
			t.Fatal("new editor did not start fresh")
		}
	}
}

func TestOldWorkAssignedAfterStartStaysHistorical(t *testing.T) {
	s, cu, store, now := fixture()
	ctx := context.Background()
	unassigned := completed("may", 1, "Arpan", now.AddDate(0, -5, 0))
	unassigned.Assignees = nil
	cu.tasks = []clickup.Task{unassigned}
	if _, err := s.Report(ctx); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	cu.tasks = []clickup.Task{completed("may", 1, "Arpan", now.AddDate(0, -5, 0)), completed("fresh", 1, "Arpan", now.Add(-time.Minute))}
	r, err := s.Report(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Editors[0].Total != 1 || r.Editors[0].HistoricalTotal != 1 || len(store.ledger.Alerts) != 0 {
		t.Fatalf("late-assigned old work entered the cycle: total=%d historical=%d", r.Editors[0].Total, r.Editors[0].HistoricalTotal)
	}
}

func TestSetCycleMovesChosenTasksIntoFirstCycle(t *testing.T) {
	s, cu, store, now := fixture()
	ctx := context.Background()
	start := *now
	cu.tasks = []clickup.Task{
		completed("a1", 1, "Arpan", start.AddDate(0, 0, -10)),
		completed("a2", 1, "Arpan", start.AddDate(0, 0, -5)),
		completed("a3", 1, "Arpan", start.AddDate(0, 0, -20)),
	}
	if _, err := s.Report(ctx); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	cu.tasks = append(cu.tasks, completed("a4", 1, "Arpan", now.Add(-time.Minute)))
	if _, err := s.Report(ctx); err != nil {
		t.Fatal(err)
	}
	// a2 was re-closed after counting began; the correction takes ClickUp's current time.
	cu.tasks[1] = completed("a2", 1, "Arpan", now.Add(-2*time.Minute))
	if _, err := s.SetCycle(ctx, "sunil", []string{"a1"}); err == nil {
		t.Fatal("accepted another editor's task")
	}
	if _, err := s.SetCycle(ctx, "arpan", []string{"a1", "missing"}); err == nil {
		t.Fatal("accepted an unknown task")
	}
	cycle, err := s.SetCycle(ctx, "arpan", []string{"a4", "a1", "a2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cycle) != 3 || cycle[0].TaskID != "a1" || cycle[1].TaskID != "a2" || cycle[2].TaskID != "a4" {
		t.Fatalf("cycle not ordered by completion: %+v", cycle)
	}
	*now = now.Add(2 * time.Minute)
	r, err := s.Report(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := r.Editors[0]
	if a.Total != 3 || a.CurrentCount != 3 || a.HistoricalTotal != 1 || a.ReviewCount != 0 || len(store.ledger.Adjustments) != 1 {
		t.Fatalf("total=%d current=%d historical=%d review=%d adjustments=%d", a.Total, a.CurrentCount, a.HistoricalTotal, a.ReviewCount, len(store.ledger.Adjustments))
	}
	if got := store.ledger.Adjustments[0].Previous; len(got) != 1 || got[0] != "a4" {
		t.Fatalf("previous cycle not audited: %v", got)
	}
	// Later work continues the corrected cycle.
	cu.tasks = append(cu.tasks, completed("a5", 1, "Arpan", now.Add(-time.Minute)))
	*now = now.Add(2 * time.Minute)
	if r, _ = s.Report(ctx); r.Editors[0].CurrentCount != 4 {
		t.Fatalf("new work did not continue the cycle: %d", r.Editors[0].CurrentCount)
	}
}
