// Package clickup is a small read-only client for the ClickUp API v2.
package clickup

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Member is a workspace member.
type Member struct {
	ID       int
	Username string
	Email    string
	Initials string
}

// DisplayName is the best human-readable name available.
func (m Member) DisplayName() string {
	if m.Username != "" {
		return m.Username
	}
	if m.Email != "" {
		return m.Email
	}
	return "user " + strconv.Itoa(m.ID)
}

// Task is the subset of a ClickUp task the bot uses.
type Task struct {
	ID          string
	Name        string
	Status      string // e.g. "in progress"
	StatusType  string // open, custom, done or closed
	Assignees   []Member
	DueDate     *time.Time // a date-only due date is normalised to the end of that day; see Dates
	DueHasTime  bool       // the due date has a time of day, not just a date
	HasBrief    bool       // the task has a description
	ListID      string
	ListName    string
	FolderName  string // empty for lists that are not in a folder
	URL         string
	DateUpdated time.Time
	DateClosed  *time.Time
	DateDone    *time.Time
}

// IsDone reports whether the task is in a done or closed status.
func (t Task) IsDone() bool {
	return t.StatusType == "closed" || t.StatusType == "done"
}

// CompletedAt is when the task was finished, if it is done.
func (t Task) CompletedAt() *time.Time {
	if !t.IsDone() {
		return nil
	}
	if t.DateClosed != nil {
		return t.DateClosed
	}
	if t.DateDone != nil {
		return t.DateDone
	}
	updated := t.DateUpdated
	return &updated
}

// AssignedTo reports whether member id is an assignee.
func (t Task) AssignedTo(id int) bool {
	for _, a := range t.Assignees {
		if a.ID == id {
			return true
		}
	}
	return false
}

// AssigneeNames returns the first names of the assignees.
func (t Task) AssigneeNames() []string {
	names := make([]string, 0, len(t.Assignees))
	for _, a := range t.Assignees {
		name := a.DisplayName()
		if first, _, ok := strings.Cut(name, " "); ok {
			name = first
		}
		names = append(names, name)
	}
	return names
}

// msTime decodes ClickUp timestamps: Unix milliseconds as a string, a number or null.
type msTime struct{ t *time.Time }

func (m *msTime) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	ms, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return nil // unexpected format: treat as unset rather than failing the page
	}
	t := time.UnixMilli(ms)
	m.t = &t
	return nil
}

type apiUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Initials string `json:"initials"`
}

func (u apiUser) member() Member {
	return Member{ID: u.ID, Username: strings.TrimSpace(u.Username), Email: u.Email, Initials: u.Initials}
}

type apiTask struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status struct {
		Status string `json:"status"`
		Type   string `json:"type"`
	} `json:"status"`
	Assignees   []apiUser `json:"assignees"`
	DueDate     msTime    `json:"due_date"`
	Description string    `json:"description"`
	DateUpdated msTime    `json:"date_updated"`
	DateClosed  msTime    `json:"date_closed"`
	DateDone    msTime    `json:"date_done"`
	URL         string    `json:"url"`
	List        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"list"`
	Folder struct {
		Name   string `json:"name"`
		Hidden bool   `json:"hidden"` // lists outside a folder sit in a hidden one
	} `json:"folder"`
}

func (a apiTask) task() Task {
	t := Task{
		ID:         a.ID,
		Name:       strings.TrimSpace(a.Name),
		Status:     a.Status.Status,
		StatusType: a.Status.Type,
		DueDate:    a.DueDate.t,
		DueHasTime: a.DueDate.t != nil,
		HasBrief:   strings.TrimSpace(a.Description) != "",
		ListID:     a.List.ID,
		ListName:   strings.TrimSpace(a.List.Name),
		URL:        a.URL,
		DateClosed: a.DateClosed.t,
		DateDone:   a.DateDone.t,
	}
	if !a.Folder.Hidden {
		t.FolderName = strings.TrimSpace(a.Folder.Name)
	}
	if a.DateUpdated.t != nil {
		t.DateUpdated = *a.DateUpdated.t
	}
	for _, u := range a.Assignees {
		t.Assignees = append(t.Assignees, u.member())
	}
	return t
}

type tasksResponse struct {
	Tasks    []apiTask       `json:"tasks"`
	LastPage json.RawMessage `json:"last_page"`
}

type teamsResponse struct {
	Teams []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Members []struct {
			User apiUser `json:"user"`
		} `json:"members"`
	} `json:"teams"`
}

// Status is one of a list's statuses. Type is open, custom, done or closed.
type Status struct {
	Name string `json:"status"`
	Type string `json:"type"`
}

// Comment is a comment on a task.
type Comment struct {
	Author Member
	Text   string
	Date   time.Time
}

// TaskDetail is what a task's details page shows: its description in
// ClickUp's markdown and its comments, oldest first.
type TaskDetail struct {
	Description string
	Comments    []Comment
}

type taskDetailResponse struct {
	MarkdownDescription string `json:"markdown_description"`
	Description         string `json:"description"`
}

type commentsResponse struct {
	Comments []struct {
		CommentText string  `json:"comment_text"`
		User        apiUser `json:"user"`
		Date        msTime  `json:"date"`
	} `json:"comments"`
}

type listResponse struct {
	Statuses []Status `json:"statuses"`
}

type userResponse struct {
	User apiUser `json:"user"`
}
