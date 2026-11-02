package approvals

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/drive"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

type memoryDrive struct {
	files    map[string]drive.File
	records  map[string][]byte
	failSave bool
	moves    int
}

func (d *memoryDrive) Children(_ context.Context, parent string) ([]drive.File, error) {
	out := []drive.File{}
	for _, f := range d.files {
		if len(f.Parents) == 1 && f.Parents[0] == parent {
			out = append(out, f)
		}
	}
	return out, nil
}
func (d *memoryDrive) File(_ context.Context, id string) (drive.File, error) {
	f, ok := d.files[id]
	if !ok {
		return f, errors.New("missing")
	}
	return f, nil
}
func (d *memoryDrive) EnsureFolder(_ context.Context, parent, name string) (drive.File, error) {
	for _, f := range d.files {
		if f.Name == name && len(f.Parents) == 1 && f.Parents[0] == parent {
			return f, nil
		}
	}
	f := drive.File{ID: randomID(), Name: name, MimeType: drive.FolderType, Parents: []string{parent}}
	d.files[f.ID] = f
	return f, nil
}
func (d *memoryDrive) CreateJSON(_ context.Context, parent, name string, v any) (drive.File, error) {
	f := drive.File{ID: randomID(), Name: name, MimeType: "application/json", Parents: []string{parent}}
	d.files[f.ID] = f
	data, _ := json.Marshal(v)
	d.records[f.ID] = data
	return f, nil
}
func (d *memoryDrive) ReadJSON(_ context.Context, id string, out any) error {
	return json.Unmarshal(d.records[id], out)
}
func (d *memoryDrive) WriteJSON(_ context.Context, id string, v any) error {
	if d.failSave {
		return errors.New("Drive unavailable")
	}
	data, err := json.Marshal(v)
	d.records[id] = data
	return err
}
func (d *memoryDrive) Move(_ context.Context, id, parent, name, description string) error {
	f := d.files[id]
	f.Name = name
	f.Parents = []string{parent}
	f.Description = description
	d.files[id] = f
	d.moves++
	return nil
}
func (d *memoryDrive) Stream(_ context.Context, id string, w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("video bytes"))
}

func fixture(t *testing.T) (*Service, *memoryDrive, Client, Client) {
	t.Helper()
	ctx := context.Background()
	loc, _ := time.LoadLocation("Europe/London")
	d := &memoryDrive{files: map[string]drive.File{drive.AuthorisedRoot: {ID: drive.AuthorisedRoot, MimeType: drive.FolderType}}, records: map[string][]byte{}}
	s, err := New(ctx, d, loc)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, loc) }
	a, err := s.AddClient(ctx, "Blanc Events", []string{"447700900111"}, "2026-11")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddClient(ctx, "Other Client", nil, "2026-11")
	if err != nil {
		t.Fatal(err)
	}
	d.files["reel"] = drive.File{ID: "reel", Name: "2026-11-05 - Venue tour.mp4", MimeType: "video/mp4", Size: "500", Parents: []string{a.Months["2026-11"].Review}}
	if err := s.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	return s, d, a, b
}

func TestFilenameDateAndRevision(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ok      bool
		version int
	}{{"2026-11-05 - Venue tour.mp4", true, 1}, {"2026-11-05 - Venue tour - v2.mp4", true, 2}, {"2026-02-30 - Venue tour.mp4", false, 0}, {"Venue tour.mp4", false, 0}, {"2026-11-05 - Venue tour.mov", false, 0}} {
		_, title, version, err := ParseFilename(tc.name)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.name, err)
		}
		if tc.ok && (version != tc.version || title != "Venue tour") {
			t.Errorf("%s: title=%q version=%d", tc.name, title, version)
		}
	}
}

func TestFeedbackIsDurableIdempotentAndClientBound(t *testing.T) {
	s, d, a, b := fixture(t)
	ctx := context.Background()
	at := 5.25
	action := Action{ReviewID: "reel", Action: "note", Note: "Replace this shot.", Version: 1, RequestID: strings.Repeat("n", 20), Timestamp: &at}
	if err := s.Act(ctx, b.ID, action); err == nil {
		t.Fatal("cross-client feedback accepted")
	}
	d.failSave = true
	if err := s.Act(ctx, a.ID, action); err == nil {
		t.Fatal("unsaved feedback reported success")
	}
	if len(s.Reviews(a.ID)[0].Notes) != 0 || d.moves != 0 {
		t.Fatal("failed save changed the review")
	}
	d.failSave = false
	if err := s.Act(ctx, a.ID, action); err != nil {
		t.Fatal(err)
	}
	if err := s.Act(ctx, a.ID, action); err != nil {
		t.Fatal(err)
	}
	review := s.Reviews(a.ID)[0]
	if len(review.Notes) != 1 || review.Notes[0].Timestamp == nil || *review.Notes[0].Timestamp != 5.25 || review.Status != "pending" {
		t.Fatalf("wrong note: %+v", review)
	}
	var restored State
	if err := json.Unmarshal(d.records[s.stateID], &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Reviews["reel"].Notes) != 1 {
		t.Fatal("feedback missing from durable state")
	}
	// A staff move to another client's folder must stop access immediately.
	f := d.files["reel"]
	f.Parents = []string{b.Months["2026-11"].Review}
	d.files["reel"] = f
	w := httptest.NewRecorder()
	s.Stream(ctx, a.ID, "reel", w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 404 {
		t.Fatal("video moved to another client remained streamable")
	}
}

func TestOnlyOneConcurrentDecisionWins(t *testing.T) {
	s, d, a, _ := fixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, kind := range []string{"approved", "changes"} {
		wg.Add(1)
		go func(i int, kind string) {
			defer wg.Done()
			results <- s.Act(context.Background(), a.ID, Action{ReviewID: "reel", Action: kind, Note: "Decision", Version: 1, RequestID: fmt.Sprintf("request-number-%020d", i)})
		}(i, kind)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 || d.moves != 1 {
		t.Fatalf("decisions=%d moves=%d", success, d.moves)
	}
	if len(s.Reviews(a.ID)[0].Notes) != 1 {
		t.Fatal("conflicting decision appended to review")
	}
}

func TestPortalSessionsCSRFAndLinkRotation(t *testing.T) {
	s, _, a, b := fixture(t)
	web, err := NewWeb(s, bytes.Repeat([]byte("k"), 32), "http://localhost", "")
	if err != nil {
		t.Fatal(err)
	}
	open := func(client Client) *http.Cookie {
		w := httptest.NewRecorder()
		web.ServeHTTP(w, httptest.NewRequest("GET", web.Link(client), nil))
		if w.Code != 303 {
			t.Fatalf("link status %d", w.Code)
		}
		return w.Result().Cookies()[0]
	}
	cookie := open(a)
	get := httptest.NewRequest("GET", "http://localhost/api/portal", nil)
	get.AddCookie(cookie)
	w := httptest.NewRecorder()
	web.ServeHTTP(w, get)
	if w.Code != 200 || strings.Contains(w.Body.String(), "447700900111") {
		t.Fatalf("client response leaked contact details: %d", w.Code)
	}
	var workspace struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &workspace); err != nil {
		t.Fatal(err)
	}
	post := func(cookie *http.Cookie, origin, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/portal/action", strings.NewReader(`{"reviewId":"reel","action":"note","note":"Feedback","version":1,"requestId":"request-http-feedback"}`))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		web.ServeHTTP(w, r)
		return w
	}
	if post(cookie, "https://elsewhere.example", workspace.CSRF).Code != 403 {
		t.Fatal("foreign-origin post accepted")
	}
	if post(cookie, "http://localhost", "wrong").Code != 403 {
		t.Fatal("bad CSRF accepted")
	}
	if post(cookie, "http://localhost", workspace.CSRF).Code != 200 {
		t.Fatal("valid feedback rejected")
	}
	other := open(b)
	otherGet := httptest.NewRequest("GET", "http://localhost/api/portal/video/reel", nil)
	otherGet.AddCookie(other)
	otherW := httptest.NewRecorder()
	web.ServeHTTP(otherW, otherGet)
	if otherW.Code != 404 {
		t.Fatal("cross-client stream accepted")
	}
	if _, err := s.Rotate(context.Background(), a.Name); err != nil {
		t.Fatal(err)
	}
	stale := httptest.NewRecorder()
	web.ServeHTTP(stale, get)
	if stale.Code != 401 {
		t.Fatal("old session survived link rotation")
	}
	staleLink := httptest.NewRecorder()
	web.ServeHTTP(staleLink, httptest.NewRequest("GET", web.Link(a), nil))
	if staleLink.Code != 401 {
		t.Fatal("old link survived rotation")
	}
}

type testMessenger struct {
	texts, templates int
	err              error
}

func (m *testMessenger) SendText(context.Context, string, string) error { m.texts++; return m.err }
func (m *testMessenger) SendTemplate(context.Context, string, string, string, []string) error {
	m.templates++
	return m.err
}

func TestPortalPaidFallbackAndUncertainDelivery(t *testing.T) {
	for _, open := range []bool{true, false} {
		t.Run(fmt.Sprintf("open=%v", open), func(t *testing.T) {
			s, _, _, _ := fixture(t)
			m := &testMessenger{}
			contacts := whatsapp.NewContacts()
			if open {
				contacts.Record("447700900111", s.now().Add(-time.Hour))
			}
			delivery := Delivery{Enabled: true, Messenger: m, Contacts: contacts, NewTemplate: "review_ready", Language: "en"}
			if err := s.SendQueued(context.Background(), delivery); err != nil {
				t.Fatal(err)
			}
			if open && (m.texts != 1 || m.templates != 0) {
				t.Fatal("open window didn't use text")
			}
			if !open && (m.texts != 0 || m.templates != 1) {
				t.Fatal("closed window didn't use approved template")
			}
			if err := s.SendQueued(context.Background(), delivery); err != nil {
				t.Fatal(err)
			}
			if m.texts+m.templates != 1 {
				t.Fatal("poll repeated notification")
			}
		})
	}
	s, _, _, _ := fixture(t)
	m := &testMessenger{err: errors.New("ambiguous network result")}
	delivery := Delivery{Enabled: true, Messenger: m, NewTemplate: "review_ready", Language: "en"}
	if err := s.SendQueued(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if err := s.SendQueued(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if m.templates != 1 || s.state.Jobs[0].State != "uncertain" {
		t.Fatal("ambiguous send was automatically repeated")
	}
}
