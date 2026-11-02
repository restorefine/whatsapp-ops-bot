// Package approvals implements the Drive-backed client video review workflow.
package approvals

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/drive"
)

type Drive interface {
	Children(context.Context, string) ([]drive.File, error)
	File(context.Context, string) (drive.File, error)
	EnsureFolder(context.Context, string, string) (drive.File, error)
	CreateJSON(context.Context, string, string, any) (drive.File, error)
	ReadJSON(context.Context, string, any) error
	WriteJSON(context.Context, string, any) error
	Move(context.Context, string, string, string, string) error
	Stream(context.Context, string, http.ResponseWriter, *http.Request)
}

type Note struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
	Kind      string    `json:"kind"`
	Timestamp *float64  `json:"timestampSeconds,omitempty"`
}

type Review struct {
	ID           string     `json:"id"`
	ClientID     string     `json:"clientId"`
	Title        string     `json:"title"`
	PostingDate  string     `json:"postingDate"`
	Version      int        `json:"version"`
	Status       string     `json:"status"`
	OriginalName string     `json:"originalName"`
	CreatedAt    time.Time  `json:"createdAt"`
	DecidedAt    *time.Time `json:"decidedAt,omitempty"`
	Notes        []Note     `json:"notes"`
	Requests     []string   `json:"requests"`
	Destination  string     `json:"destination,omitempty"`
	MovePending  bool       `json:"movePending,omitempty"`
	NotifiedAt   *time.Time `json:"notifiedAt,omitempty"`
	ReminderAt   *time.Time `json:"reminderAt,omitempty"`
	UrgentQueued bool       `json:"urgentQueued,omitempty"`
}

type Folders struct {
	Review   string `json:"review"`
	Approved string `json:"approved"`
	Changes  string `json:"changes"`
}
type Client struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Initials   string             `json:"initials"`
	Numbers    []string           `json:"numbers"`
	Folder     string             `json:"folder"`
	Months     map[string]Folders `json:"months"`
	Generation int                `json:"generation"`
}

type Job struct {
	ID        string    `json:"id"`
	ClientID  string    `json:"clientId"`
	ReviewIDs []string  `json:"reviewIds"`
	To        string    `json:"to"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
	Error     string    `json:"error,omitempty"`
}

type State struct {
	Schema  int                `json:"schema"`
	Clients map[string]*Client `json:"clients"`
	Reviews map[string]*Review `json:"reviews"`
	Jobs    []*Job             `json:"jobs"`
	Invalid map[string]string  `json:"invalid"`
}

type Service struct {
	mu            sync.Mutex
	drive         Drive
	state         State
	stateID       string
	clientsFolder string
	root          string
	loc           *time.Location
	now           func() time.Time
	Staff         []string
	Manager       string
	access        atomic.Pointer[accessSnapshot]
	needsReload   bool
}

type accessSnapshot struct {
	clients map[string]Client
	numbers map[string]bool
}

func (s *Service) updateAccess() {
	v := &accessSnapshot{clients: map[string]Client{}, numbers: map[string]bool{}}
	for id, c := range s.state.Clients {
		v.clients[id] = Client{ID: c.ID, Name: c.Name, Initials: c.Initials, Generation: c.Generation}
		for _, n := range c.Numbers {
			v.numbers[n] = true
		}
	}
	s.access.Store(v)
}

func (s *Service) prepare(ctx context.Context) error {
	if !s.needsReload {
		return nil
	}
	var restored State
	if err := s.drive.ReadJSON(ctx, s.stateID, &restored); err != nil {
		return errors.New("Drive state is recovering; please try again shortly")
	}
	if err := validateState(restored); err != nil {
		return err
	}
	s.state = restored
	s.needsReload = false
	s.updateAccess()
	return nil
}

func validateState(state State) error {
	if state.Schema != 1 || state.Clients == nil || state.Reviews == nil || state.Invalid == nil {
		return errors.New("unsupported or incomplete portal state")
	}
	for id, c := range state.Clients {
		if c == nil || c.ID != id || c.Generation < 1 || c.Folder == "" || c.Months == nil {
			return errors.New("invalid portal client record")
		}
		for month, f := range c.Months {
			if _, err := time.Parse("2006-01", month); err != nil || f.Review == "" || f.Approved == "" || f.Changes == "" || f.Review == f.Approved || f.Review == f.Changes || f.Approved == f.Changes {
				return errors.New("invalid client month folders")
			}
		}
	}
	for id, r := range state.Reviews {
		if r == nil || r.ID != id || state.Clients[r.ClientID] == nil || len(r.PostingDate) != 10 {
			return errors.New("invalid portal review record")
		}
		if _, err := time.Parse("2006-01-02", r.PostingDate); err != nil {
			return errors.New("invalid review posting date")
		}
		f, ok := state.Clients[r.ClientID].Months[r.PostingDate[:7]]
		if !ok {
			return errors.New("review month is missing")
		}
		if r.Status != "pending" && r.Status != "approved" && r.Status != "changes" && r.Status != "superseded" {
			return errors.New("invalid review status")
		}
		if r.MovePending {
			expected := f.Approved
			if r.Status == "changes" {
				expected = f.Changes
			}
			if (r.Status != "approved" && r.Status != "changes") || r.DecidedAt == nil || r.Destination != expected {
				return errors.New("invalid pending decision move")
			}
		}
	}
	for _, j := range state.Jobs {
		if j == nil || state.Clients[j.ClientID] == nil {
			return errors.New("invalid notification record")
		}
	}
	return nil
}

func New(ctx context.Context, d Drive, loc *time.Location) (*Service, error) {
	if loc == nil {
		return nil, errors.New("approval timezone is required")
	}
	s := &Service{drive: d, root: drive.AuthorisedRoot, loc: loc, now: time.Now}
	// A fresh state is created only inside the explicitly authorised, initially
	// empty root. Nothing outside it is discovered or imported.
	system, err := d.EnsureFolder(ctx, s.root, "_Portal")
	if err != nil {
		return nil, err
	}
	clients, err := d.EnsureFolder(ctx, s.root, "Clients")
	if err != nil {
		return nil, err
	}
	s.clientsFolder = clients.ID
	children, err := d.Children(ctx, system.ID)
	if err != nil {
		return nil, err
	}
	for _, f := range children {
		if f.Name == "state.json" {
			if s.stateID != "" {
				return nil, errors.New("duplicate portal state files")
			}
			s.stateID = f.ID
		}
	}
	if s.stateID != "" {
		if err := d.ReadJSON(ctx, s.stateID, &s.state); err != nil {
			return nil, err
		}
	} else {
		s.state = State{Schema: 1, Clients: map[string]*Client{}, Reviews: map[string]*Review{}, Invalid: map[string]string{}, Jobs: []*Job{}}
		f, err := d.CreateJSON(ctx, system.ID, "state.json", &s.state)
		if err != nil {
			return nil, err
		}
		s.stateID = f.ID
	}
	if s.state.Invalid == nil {
		s.state.Invalid = map[string]string{}
	}
	if err := validateState(s.state); err != nil {
		return nil, err
	}
	recovered := false
	for _, job := range s.state.Jobs {
		if job.State == "sending" {
			job.State = "uncertain"
			job.Error = "Delivery may have completed before restart; check before retrying."
			recovered = true
		}
	}
	if recovered {
		if err := s.save(ctx); err != nil {
			return nil, err
		}
	}
	s.updateAccess()
	return s, nil
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (s *Service) save(ctx context.Context) error {
	err := s.drive.WriteJSON(ctx, s.stateID, &s.state)
	if err != nil {
		s.needsReload = true
	}
	return err
}

// transaction restores the in-memory state after a failed durable save. Drive
// moves are performed only after a successful save of their durable intent.
func (s *Service) transaction(ctx context.Context, f func() error) error {
	before, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	rollback := func() { var original State; _ = json.Unmarshal(before, &original); s.state = original }
	if err := f(); err != nil {
		rollback()
		return err
	}
	after, err := json.Marshal(s.state)
	if err != nil {
		rollback()
		return err
	}
	if string(before) == string(after) {
		return nil
	}
	if err := s.save(ctx); err != nil {
		rollback()
		return err
	}
	s.updateAccess()
	return nil
}

var numberPattern = regexp.MustCompile(`^[1-9][0-9]{7,14}$`)

func (s *Service) AddClient(ctx context.Context, name string, numbers []string, month string) (Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return Client{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return Client{}, errors.New("use a short client name without path separators")
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		return Client{}, errors.New("month must use YYYY-MM")
	}
	for _, n := range numbers {
		if !numberPattern.MatchString(n) {
			return Client{}, errors.New("WhatsApp numbers need country code and digits only")
		}
	}
	for _, client := range s.state.Clients {
		if strings.EqualFold(client.Name, name) {
			if err := s.transaction(ctx, func() error {
				if numbers != nil {
					client.Numbers = numbers
				}
				return s.ensureMonth(ctx, client, month)
			}); err != nil {
				return Client{}, err
			}
			return *client, nil
		}
	}
	folder, err := s.drive.EnsureFolder(ctx, s.clientsFolder, name)
	if err != nil {
		return Client{}, err
	}
	words := strings.Fields(name)
	initials := ""
	for _, w := range words {
		r, _ := utf8.DecodeRuneInString(w)
		initials += strings.ToUpper(string(r))
		if utf8.RuneCountInString(initials) >= 2 {
			break
		}
	}
	client := &Client{ID: randomID(), Name: name, Initials: initials, Numbers: numbers, Folder: folder.ID, Months: map[string]Folders{}, Generation: 1}
	if err := s.ensureMonth(ctx, client, month); err != nil {
		return Client{}, err
	}
	if err := s.transaction(ctx, func() error { s.state.Clients[client.ID] = client; return nil }); err != nil {
		return Client{}, err
	}
	return *client, nil
}

func (s *Service) ensureMonth(ctx context.Context, client *Client, month string) error {
	if _, ok := client.Months[month]; ok {
		return nil
	}
	folder, err := s.drive.EnsureFolder(ctx, client.Folder, month)
	if err != nil {
		return err
	}
	ids := []string{}
	for _, name := range []string{"01 - For review", "02 - Approved", "03 - Changes needed"} {
		f, err := s.drive.EnsureFolder(ctx, folder.ID, name)
		if err != nil {
			return err
		}
		ids = append(ids, f.ID)
	}
	client.Months[month] = Folders{Review: ids[0], Approved: ids[1], Changes: ids[2]}
	return nil
}

var filenamePattern = regexp.MustCompile(`(?i)^([0-9]{4}-[0-9]{2}-[0-9]{2}) - (.+?)(?: - v([1-9][0-9]*))?\.(mp4|webm)$`)

func ParseFilename(name string) (date, title string, version int, err error) {
	m := filenamePattern.FindStringSubmatch(name)
	if m == nil {
		return "", "", 0, errors.New("name previews YYYY-MM-DD - Video title.mp4; revisions end with - v2.mp4")
	}
	if _, err = time.Parse("2006-01-02", m[1]); err != nil {
		return "", "", 0, errors.New("filename contains an invalid posting date")
	}
	version = 1
	if m[3] != "" {
		version, err = strconv.Atoi(m[3])
		if err != nil || version > 1000 {
			return "", "", 0, errors.New("invalid revision number")
		}
	}
	title = strings.TrimSpace(m[2])
	if title == "" || utf8.RuneCountInString(title) > 180 {
		return "", "", 0, errors.New("video title must contain 1 to 180 characters")
	}
	return m[1], title, version, nil
}

func (s *Service) Poll(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return err
	}
	return s.transaction(ctx, func() error {
		s.pruneJobs()
		for _, client := range s.state.Clients {
			// Provision the current and next month inside each registered client.
			for _, month := range []string{s.now().In(s.loc).Format("2006-01"), s.now().In(s.loc).AddDate(0, 1, 0).Format("2006-01")} {
				if err := s.ensureMonth(ctx, client, month); err != nil {
					return err
				}
			}
			for month, folders := range client.Months {
				files, err := s.drive.Children(ctx, folders.Review)
				if err != nil {
					return err
				}
				for _, file := range files {
					if file.MimeType == drive.FolderType || !strings.HasPrefix(file.MimeType, "video/") {
						continue
					}
					if existing, exists := s.state.Reviews[file.ID]; exists {
						if existing.ClientID != client.ID {
							continue
						}
						if existing.Status == "pending" {
							date, title, version, parseErr := ParseFilename(file.Name)
							if parseErr == nil && strings.HasPrefix(date, month+"-") && version == existing.Version {
								if existing.OriginalName != file.Name {
									s.cancelQueued(existing.ID)
									existing.UrgentQueued = false
									existing.ReminderAt = nil
								}
								existing.Title, existing.PostingDate, existing.OriginalName = title, date, file.Name
								delete(s.state.Invalid, file.ID)
								if existing.NotifiedAt == nil {
									active := false
									for _, j := range s.state.Jobs {
										if j.Kind != "new" || j.State == "cancelled" {
											continue
										}
										for _, ref := range j.ReviewIDs {
											active = active || ref == existing.ID
										}
									}
									if !active {
										s.enqueue(client.ID, []string{existing.ID}, client.Numbers, "new", "Video ready for review")
									}
								}
							} else {
								s.state.Invalid[file.ID] = "renamed preview needs a valid date in its month and the same revision number"
								s.cancelQueued(file.ID)
							}
						}
						continue
					}
					date, title, version, err := ParseFilename(file.Name)
					if err == nil && !strings.HasPrefix(date, month+"-") {
						err = errors.New("filename posting date does not match its month folder")
					}
					if err == nil {
						for _, old := range s.state.Reviews {
							if old.ClientID == client.ID && strings.EqualFold(old.Title, title) && old.PostingDate == date && old.Version >= version {
								err = errors.New("duplicate or older revision; use a new explicit - v2 suffix")
								break
							}
						}
					}
					if err != nil {
						if s.state.Invalid[file.ID] != err.Error() {
							s.state.Invalid[file.ID] = err.Error()
							s.enqueue(client.ID, nil, s.Staff, "internal", client.Name+": "+file.Name+". "+err.Error())
						}
						continue
					}
					if file.Size == "0" {
						continue
					}
					for _, old := range s.state.Reviews {
						if old.ClientID == client.ID && old.PostingDate == date && strings.EqualFold(old.Title, title) && old.Status == "pending" {
							old.Status = "superseded"
							s.cancelQueued(old.ID)
						}
					}
					now := s.now()
					s.state.Reviews[file.ID] = &Review{ID: file.ID, ClientID: client.ID, Title: title, PostingDate: date, Version: version, Status: "pending", OriginalName: file.Name, CreatedAt: now, Notes: []Note{}, Requests: []string{}}
					delete(s.state.Invalid, file.ID)
					s.enqueue(client.ID, []string{file.ID}, client.Numbers, "new", fmt.Sprintf("%s: a new video is ready for review. %s, posting %s.", client.Name, title, date))
				}
			}
		}
		return nil
	})
}

func (s *Service) enqueue(clientID string, reviewIDs, recipients []string, kind, text string) {
	seen := map[string]bool{}
	for _, to := range recipients {
		if to != "" && !seen[to] {
			s.state.Jobs = append(s.state.Jobs, &Job{ID: randomID(), ClientID: clientID, ReviewIDs: reviewIDs, To: to, Kind: kind, Text: text, State: "queued", CreatedAt: s.now()})
			seen[to] = true
		}
	}
}

type Action struct {
	ReviewID  string   `json:"reviewId"`
	Action    string   `json:"action"`
	Note      string   `json:"note"`
	Version   int      `json:"version"`
	RequestID string   `json:"requestId"`
	Timestamp *float64 `json:"timestampSeconds"`
}

func (s *Service) Act(ctx context.Context, clientID string, action Action) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return err
	}
	review, ok := s.state.Reviews[action.ReviewID]
	if !ok || review.ClientID != clientID {
		return errors.New("video not found in this workspace")
	}
	if s.state.Invalid[review.ID] != "" {
		return errors.New("this video needs its filename corrected before review")
	}
	if len(action.RequestID) < 16 || len(action.RequestID) > 100 {
		return errors.New("invalid feedback request")
	}
	for _, id := range review.Requests {
		if id == action.RequestID {
			return nil
		}
	}
	if review.Status != "pending" || review.Version != action.Version {
		return errors.New("this version is no longer waiting for review; refresh your videos")
	}
	if action.Action != "note" && action.Action != "approved" && action.Action != "changes" {
		return errors.New("unknown review action")
	}
	action.Note = strings.TrimSpace(action.Note)
	if utf8.RuneCountInString(action.Note) > 4000 || !utf8.ValidString(action.Note) {
		return errors.New("notes must contain at most 4000 characters")
	}
	if action.Action != "approved" && action.Note == "" {
		return errors.New("add a note explaining your feedback")
	}
	if action.Timestamp != nil && (math.IsNaN(*action.Timestamp) || math.IsInf(*action.Timestamp, 0) || *action.Timestamp < 0 || *action.Timestamp > 24*60*60) {
		return errors.New("invalid playback timestamp")
	}
	file, err := s.drive.File(ctx, review.ID)
	if err != nil || !strings.HasPrefix(file.MimeType, "video/") || len(file.Parents) != 1 || file.Parents[0] != s.state.Clients[clientID].Months[review.PostingDate[:7]].Review {
		return errors.New("video is no longer available in the review folder")
	}
	client := s.state.Clients[clientID]
	err = s.transaction(ctx, func() error {
		now := s.now()
		review.Requests = append(review.Requests, action.RequestID)
		review.Notes = append(review.Notes, Note{ID: randomID(), Text: action.Note, Author: "Client reviewer", CreatedAt: now, Kind: action.Action, Timestamp: action.Timestamp})
		if action.Action != "note" {
			review.Status = action.Action
			review.DecidedAt = &now
			folders := client.Months[review.PostingDate[:7]]
			review.Destination = folders.Approved
			if action.Action == "changes" {
				review.Destination = folders.Changes
			}
			review.MovePending = true
			s.cancelQueued(review.ID)
		}
		message := fmt.Sprintf("%s: %s - %s", client.Name, review.Title, map[string]string{"note": "new feedback", "approved": "approved", "changes": "changes requested"}[action.Action])
		if action.Timestamp != nil {
			message += fmt.Sprintf(" at %02d:%02d", int(*action.Timestamp)/60, int(*action.Timestamp)%60)
		}
		if action.Note != "" {
			message += "\n" + action.Note
		}
		s.enqueue(clientID, []string{review.ID}, []string{s.Manager}, "internal", message)
		return nil
	})
	if err != nil {
		return err
	}
	// The decision is durable before its move. A failed move is retried by Run;
	// it does not ask the client to submit the same decision again.
	_ = s.reconcile(ctx)
	return nil
}

func (s *Service) reconcile(ctx context.Context) error {
	changed := false
	for _, review := range s.state.Reviews {
		if !review.MovePending {
			continue
		}
		file, err := s.drive.File(ctx, review.ID)
		if err != nil {
			return err
		}
		folders := s.state.Clients[review.ClientID].Months[review.PostingDate[:7]]
		if len(file.Parents) != 1 || (file.Parents[0] != folders.Review && file.Parents[0] != review.Destination) {
			return errors.New("decision move held: video was moved outside its client's review folders")
		}
		stamp := review.DecidedAt.In(s.loc).Format("2006-01-02 15-04 MST")
		prefix := "APPROVED"
		if review.Status == "changes" {
			prefix = "CHANGES"
		}
		description := ""
		if len(review.Notes) > 0 {
			description = review.Notes[len(review.Notes)-1].Text
		}
		if err := s.drive.Move(ctx, review.ID, review.Destination, prefix+" "+stamp+" - "+review.OriginalName, description); err != nil {
			return err
		}
		review.MovePending = false
		changed = true
	}
	if changed {
		return s.save(ctx)
	}
	return nil
}

// Client returns a copy suitable for authentication checks, never mutable state.
func (s *Service) Client(id string) (Client, bool) {
	v := s.access.Load()
	if v == nil {
		return Client{}, false
	}
	c, ok := v.clients[id]
	return c, ok
}
func (s *Service) KnownNumber(number string) bool {
	v := s.access.Load()
	return v != nil && v.numbers[number]
}

func (s *Service) Reviews(clientID string) []Review {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []Review{}
	for _, r := range s.state.Reviews {
		if r.ClientID == clientID && r.Status != "superseded" && s.state.Invalid[r.ID] == "" {
			copy := *r
			copy.Notes = append([]Note{}, r.Notes...)
			result = append(result, copy)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PostingDate < result[j].PostingDate })
	return result
}

func (s *Service) Stream(ctx context.Context, clientID, id string, w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	review, ok := s.state.Reviews[id]
	allowed := ok && review.ClientID == clientID
	var folders Folders
	if allowed {
		folders = s.state.Clients[clientID].Months[review.PostingDate[:7]]
	}
	s.mu.Unlock()
	if !allowed {
		http.NotFound(w, r)
		return
	}
	file, err := s.drive.File(ctx, id)
	if err != nil || len(file.Parents) != 1 || (file.Parents[0] != folders.Review && file.Parents[0] != folders.Approved && file.Parents[0] != folders.Changes) {
		http.NotFound(w, r)
		return
	}
	s.drive.Stream(ctx, id, w, r)
}

func (s *Service) Rotate(ctx context.Context, name string) (Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return Client{}, err
	}
	for _, c := range s.state.Clients {
		if strings.EqualFold(c.Name, strings.TrimSpace(name)) {
			if err := s.transaction(ctx, func() error { c.Generation++; return nil }); err != nil {
				return Client{}, err
			}
			return *c, nil
		}
	}
	return Client{}, errors.New("client not found")
}

func (s *Service) cancelQueued(id string) {
	for _, j := range s.state.Jobs {
		if j.State != "queued" || j.Kind == "internal" {
			continue
		}
		kept := make([]string, 0, len(j.ReviewIDs))
		for _, ref := range j.ReviewIDs {
			if ref != id {
				kept = append(kept, ref)
			}
		}
		j.ReviewIDs = kept
		if len(kept) == 0 {
			j.State = "cancelled"
		}
	}
}
