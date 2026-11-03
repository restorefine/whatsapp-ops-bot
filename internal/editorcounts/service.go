// Package editorcounts records completed editor tasks in rolling batches of 20.
// It reads ClickUp; it never changes tasks or records a payment.
package editorcounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
)

const BatchSize = 20

type Editor struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Number string `json:"number,omitempty"`
}

type Record struct {
	TaskID      string    `json:"taskId"`
	Title       string    `json:"title"`
	List        string    `json:"list"`
	URL         string    `json:"url"`
	CompletedAt time.Time `json:"completedAt"`
	RecordedAt  time.Time `json:"recordedAt"`
	Status      string    `json:"status"`
	Batch       int       `json:"batch"`
	Position    int       `json:"position"`
	Imported    bool      `json:"imported"`
	Issue       string    `json:"issue,omitempty"`
}

// Ledger is persisted alongside portal state in Drive. Entries are never removed
// or moved to another batch when a task is reopened, renamed or reassigned.
type Ledger struct {
	StartedAt time.Time            `json:"startedAt"`
	CheckedAt time.Time            `json:"checkedAt"`
	Editors   map[string][]Record  `json:"editors"`
	Joined    map[string]time.Time `json:"joined"`
	Alerts    []Alert              `json:"alerts"`
}

type Alert struct {
	ID        string    `json:"id"`
	EditorKey string    `json:"editorKey"`
	Batch     int       `json:"batch"`
	To        string    `json:"to"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
}

type Store interface {
	UpdateEditorLedger(context.Context, func(*Ledger) error) error
}

type Source interface {
	Members(context.Context) ([]clickup.Member, error)
	Tasks(context.Context, clickup.TaskFilter) ([]clickup.Task, error)
}

type EditorReport struct {
	Editor
	Total           int       `json:"total"`
	FullBatches     int       `json:"fullBatches"`
	CurrentBatch    int       `json:"currentBatch"`
	CurrentCount    int       `json:"currentCount"`
	Remaining       int       `json:"remaining"`
	ReviewCount     int       `json:"reviewCount"`
	HistoricalTotal int       `json:"historicalTotal"`
	StartedAt       time.Time `json:"startedAt"`
	Inactive        bool      `json:"inactive"`
	Records         []Record  `json:"records"`
	Warnings        []string  `json:"warnings"`
}

type Report struct {
	StartedAt time.Time      `json:"startedAt"`
	CheckedAt time.Time      `json:"checkedAt"`
	BatchSize int            `json:"batchSize"`
	Editors   []EditorReport `json:"editors"`
	Alerts    []Alert        `json:"alerts"`
}

type Service struct {
	Source     Source
	Store      Store
	Editors    []Editor
	Admins     []string
	Now        func() time.Time
	mu         sync.Mutex
	deliveryMu sync.Mutex
	cached     *Report
}

func (s *Service) Report(ctx context.Context) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now().UTC()
	if s.cached != nil && now.Sub(s.cached.CheckedAt) < time.Minute {
		return clone(*s.cached), nil
	}
	members, err := s.Source.Members(ctx)
	if err != nil {
		return Report{}, err
	}
	ids := map[string]int{}
	filter := clickup.TaskFilter{IncludeClosed: true}
	if len(s.Editors) == 0 {
		return Report{}, errors.New("no video editors configured")
	}
	for _, editor := range s.Editors {
		for _, m := range members {
			first := strings.Fields(strings.ToLower(m.DisplayName()))
			if strings.EqualFold(m.Username, editor.Key) || (len(first) > 0 && first[0] == editor.Key) {
				if ids[editor.Key] != 0 {
					return Report{}, fmt.Errorf("more than one ClickUp member matches %s", editor.Name)
				}
				ids[editor.Key] = m.ID
			}
		}
		if ids[editor.Key] == 0 {
			return Report{}, fmt.Errorf("no ClickUp member matches %s", editor.Name)
		}
		filter.Assignees = append(filter.Assignees, ids[editor.Key])
	}
	tasks, err := s.Source.Tasks(ctx, filter)
	if err != nil {
		return Report{}, err
	}
	byID := map[string]clickup.Task{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	var report Report
	err = s.Store.UpdateEditorLedger(ctx, func(ledger *Ledger) error {
		if err := Validate(*ledger); err != nil {
			return err
		}
		if ledger.StartedAt.IsZero() {
			ledger.StartedAt = now
		}
		if ledger.Editors == nil {
			ledger.Editors = map[string][]Record{}
		}
		if ledger.Joined == nil {
			ledger.Joined = map[string]time.Time{}
		}
		seen := map[string]bool{}
		for _, records := range ledger.Editors {
			for _, r := range records {
				seen[r.TaskID] = true
			}
		}
		report = Report{StartedAt: ledger.StartedAt, CheckedAt: now, BatchSize: BatchSize, Editors: []EditorReport{}}
		for _, editor := range s.Editors {
			initial := ledger.Joined[editor.Key].IsZero()
			if initial {
				ledger.Joined[editor.Key] = now
			}
			memberID := ids[editor.Key]
			warnings := []string{}
			records := ledger.Editors[editor.Key]
			oldTotal := 0
			for _, r := range records {
				if !r.Imported {
					oldTotal++
				}
			}
			for i := range records {
				r := &records[i]
				task, ok := byID[r.TaskID]
				r.Issue = ""
				if !ok {
					r.Issue = "Task no longer returned by ClickUp. Verify its original evidence link."
					continue
				}
				if eligible(task, editor.Key, ids) != editor.Key {
					r.Issue = "Task is no longer uniquely assigned, in this editor's list, and complete."
				}
				if completed := completion(task); completed == nil || !completed.Equal(r.CompletedAt) {
					r.Issue = "Completion timestamp changed or disappeared; original evidence retained."
				}
			}
			var candidates []clickup.Task
			for _, task := range byID {
				if seen[task.ID] || !task.AssignedTo(memberID) || !strings.EqualFold(task.ListName, editor.Key) || task.ParentID != "" {
					continue
				}
				if !isComplete(task) {
					continue
				}
				if eligible(task, editor.Key, ids) == "" {
					warnings = append(warnings, task.Name+": multiple editors assigned; not counted ("+task.ID+")")
					continue
				}
				if completion(task) == nil {
					warnings = append(warnings, task.Name+": no completion timestamp; not counted ("+task.ID+")")
					continue
				}
				if completion(task).After(now) {
					warnings = append(warnings, task.Name+": future completion timestamp; not counted ("+task.ID+")")
					continue
				}
				candidates = append(candidates, task)
			}
			sort.Slice(candidates, func(i, j int) bool {
				a, b := completion(candidates[i]), completion(candidates[j])
				if a.Equal(*b) {
					return candidates[i].ID < candidates[j].ID
				}
				return a.Before(*b)
			})
			index := oldTotal
			for _, task := range candidates {
				batch, position := index/BatchSize+1, index%BatchSize+1
				if initial {
					batch, position = 0, 0
				}
				records = append(records, Record{TaskID: task.ID, Title: task.Name, List: task.ListName, URL: "https://app.clickup.com/t/" + url.PathEscape(task.ID), CompletedAt: *completion(task), RecordedAt: now, Status: task.Status, Batch: batch, Position: position, Imported: initial})
				seen[task.ID] = true
				if !initial {
					index++
				}
			}
			ledger.Editors[editor.Key] = records
			total, historical := 0, 0
			for _, r := range records {
				if r.Imported {
					historical++
				} else {
					total++
				}
			}
			er := EditorReport{Editor: editor, Total: total, FullBatches: total / BatchSize, CurrentBatch: total/BatchSize + 1, CurrentCount: total % BatchSize, Remaining: BatchSize - total%BatchSize, HistoricalTotal: historical, StartedAt: ledger.Joined[editor.Key], Records: append([]Record{}, records...), Warnings: warnings}
			for batch := oldTotal/BatchSize + 1; batch <= total/BatchSize; batch++ {
				for _, admin := range s.Admins {
					ledger.Alerts = append(ledger.Alerts, Alert{ID: fmt.Sprintf("%s:%d:%s", editor.Key, batch, admin), EditorKey: editor.Key, Batch: batch, To: admin, State: "queued", CreatedAt: now})
				}
			}
			for _, r := range records {
				if r.Issue != "" {
					er.ReviewCount++
				}
			}
			sort.Strings(er.Warnings)
			report.Editors = append(report.Editors, er)
		}
		active := map[string]bool{}
		for _, editor := range s.Editors {
			active[editor.Key] = true
		}
		for key, records := range ledger.Editors {
			if active[key] {
				continue
			}
			total, historical := 0, 0
			for _, r := range records {
				if r.Imported {
					historical++
				} else {
					total++
				}
			}
			report.Editors = append(report.Editors, EditorReport{Editor: Editor{Key: key, Name: key}, Inactive: true, Total: total, HistoricalTotal: historical, FullBatches: total / BatchSize, CurrentBatch: total/BatchSize + 1, CurrentCount: total % BatchSize, Remaining: BatchSize - total%BatchSize, Records: append([]Record{}, records...), StartedAt: ledger.Joined[key], Warnings: []string{"Removed from the current roster. Audit history retained; no new tasks counted."}})
		}
		sort.SliceStable(report.Editors, func(i, j int) bool {
			if report.Editors[i].Inactive != report.Editors[j].Inactive {
				return !report.Editors[i].Inactive
			}
			return report.Editors[i].Key < report.Editors[j].Key
		})
		report.Alerts = append([]Alert{}, ledger.Alerts...)
		ledger.CheckedAt = now
		return Validate(*ledger)
	})
	if err != nil {
		return Report{}, err
	}
	s.cached = &report
	return clone(report), nil
}

func isComplete(t clickup.Task) bool {
	if !t.IsDone() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(t.Status)) {
	case "complete", "completed", "done":
		return true
	}
	return false
}

func completion(t clickup.Task) *time.Time {
	if t.DateClosed != nil && !t.DateClosed.IsZero() {
		return t.DateClosed
	}
	if t.DateDone != nil && !t.DateDone.IsZero() {
		return t.DateDone
	}
	return nil // UpdatedAt is an edit timestamp, not proof of completion.
}

func eligible(t clickup.Task, key string, ids map[string]int) string {
	if t.ParentID != "" || !strings.EqualFold(t.ListName, key) || !isComplete(t) || !t.AssignedTo(ids[key]) {
		return ""
	}
	count := 0
	for _, id := range ids {
		if t.AssignedTo(id) {
			count++
		}
	}
	if count != 1 {
		return ""
	}
	return key
}

func Validate(l Ledger) error {
	seen := map[string]bool{}
	for _, records := range l.Editors {
		index := 0
		for _, r := range records {
			if r.TaskID == "" || seen[r.TaskID] || r.CompletedAt.IsZero() || r.RecordedAt.IsZero() {
				return errors.New("editor audit ledger is invalid; counts were not changed")
			}
			if r.Imported {
				if r.Batch != 0 || r.Position != 0 {
					return errors.New("historical record entered an active cycle")
				}
			} else {
				if r.Batch != index/BatchSize+1 || r.Position != index%BatchSize+1 {
					return errors.New("invalid editor batch sequence")
				}
				index++
			}
			seen[r.TaskID] = true
		}
	}
	alerts := map[string]bool{}
	for _, a := range l.Alerts {
		if alerts[a.ID] || a.ID != fmt.Sprintf("%s:%d:%s", a.EditorKey, a.Batch, a.To) || a.Batch < 1 || a.CreatedAt.IsZero() {
			return errors.New("invalid editor notification receipt")
		}
		switch a.State {
		case "queued", "sending", "sent", "uncertain", "cancelled":
		default:
			return errors.New("invalid editor notification state")
		}
		alerts[a.ID] = true
	}
	return nil
}

func clone(r Report) Report {
	data, _ := json.Marshal(r)
	var copy Report
	_ = json.Unmarshal(data, &copy)
	return copy
}
