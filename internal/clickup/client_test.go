package clickup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestTasksPaginatesAndParses(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "pk_token" {
			t.Errorf("authorization header = %q, want the raw token", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/team/42/task" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("subtasks") != "true" || q.Get("include_closed") != "true" || q.Get("due_date_gt") != "1000" || q.Get("assignees[]") != "7" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		switch q.Get("page") {
		case "0":
			fmt.Fprint(rw, `{"tasks":[{"id":"a","name":" Design homepage ","status":{"status":"in progress","type":"custom"},
				"assignees":[{"id":7,"username":"Alice Smith"}],"due_date":"1790000000000","date_updated":"1790000000000",
				"date_closed":null,"url":"https://app.clickup.com/t/a","list":{"name":"Website"}}],"last_page":false}`)
		case "1":
			fmt.Fprint(rw, `{"tasks":[{"id":"b","name":"Ship","status":{"status":"complete","type":"closed"},
				"assignees":[],"due_date":null,"date_updated":"1790000000000","date_closed":"1790000500000"}],"last_page":true}`)
		default:
			t.Errorf("unexpected page %s", q.Get("page"))
		}
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "pk_token", "42", quiet)
	f := TaskFilter{DueAfter: time.UnixMilli(1000), Assignees: []int{7}, IncludeClosed: true}
	tasks, err := c.Tasks(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks", len(tasks))
	}
	a, b := tasks[0], tasks[1]
	if a.Name != "Design homepage" || a.ListName != "Website" || a.DueDate == nil || a.DueDate.UnixMilli() != 1790000000000 || a.IsDone() {
		t.Errorf("task a = %+v", a)
	}
	if !a.AssignedTo(7) || a.AssigneeNames()[0] != "Alice" {
		t.Errorf("assignees = %+v", a.Assignees)
	}
	if !b.IsDone() || b.DueDate != nil || b.CompletedAt().UnixMilli() != 1790000500000 {
		t.Errorf("task b = %+v", b)
	}

	// Second identical call is served from cache.
	if _, err := c.Tasks(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("made %d requests, want 2 (cache should serve the repeat)", calls.Load())
	}
}

func TestRateLimitRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			rw.Header().Set("Retry-After", "0")
			rw.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(rw, `{"teams":[{"id":"42","members":[{"user":{"id":7,"username":"Alice Smith"}},{"user":{"id":8,"username":"Bob"}}]}]}`)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "pk", "42", quiet)
	members, err := c.Members(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || calls.Load() != 2 {
		t.Fatalf("members=%v calls=%d", members, calls.Load())
	}
}

func TestAPIErrorAndUnknownTeam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/team" {
			fmt.Fprint(rw, `{"teams":[{"id":"1"}]}`)
			return
		}
		rw.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(rw, `{"err":"Token invalid","ECODE":"OAUTH_025"}`)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "bad", "42", quiet)
	if _, err := c.Tasks(context.Background(), TaskFilter{}); err == nil || err.Error() != "clickup: http 401: Token invalid (OAUTH_025)" {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Members(context.Background()); err == nil {
		t.Fatal("expected unknown team error")
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Unix(1000, 0)
	tests := []struct {
		header http.Header
		want   time.Duration
	}{
		{http.Header{"Retry-After": {"7"}}, 7 * time.Second},
		{http.Header{"X-Ratelimit-Reset": {"1010"}}, 11 * time.Second},
		{http.Header{}, 5 * time.Second},
	}
	for _, tt := range tests {
		if got := retryAfter(tt.header, now); got != tt.want {
			t.Errorf("%v: got %v, want %v", tt.header, got, tt.want)
		}
	}
}
