package clickup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client is what the command logic needs from ClickUp. Tests use a fake.
// Apart from SetStatus and AddComment, which mark tasks complete, it only reads.
type Client interface {
	Tasks(ctx context.Context, f TaskFilter) ([]Task, error)
	Members(ctx context.Context) ([]Member, error)
	TaskDetail(ctx context.Context, taskID string) (TaskDetail, error)
	ListStatuses(ctx context.Context, listID string) ([]Status, error)
	SetStatus(ctx context.Context, taskID, status string) error
	AddComment(ctx context.Context, taskID, text string) error
	Me(ctx context.Context) (Member, error)
}

// TaskFilter maps to query parameters of GET /team/{team_id}/task.
// Zero values are left out.
type TaskFilter struct {
	DueAfter      time.Time
	DueBefore     time.Time
	UpdatedAfter  time.Time
	Assignees     []int
	IncludeClosed bool
}

func (f TaskFilter) values() url.Values {
	v := url.Values{}
	v.Set("subtasks", "true")
	v.Set("include_closed", strconv.FormatBool(f.IncludeClosed))
	ms := func(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }
	if !f.DueAfter.IsZero() {
		v.Set("due_date_gt", ms(f.DueAfter))
	}
	if !f.DueBefore.IsZero() {
		v.Set("due_date_lt", ms(f.DueBefore))
	}
	if !f.UpdatedAfter.IsZero() {
		v.Set("date_updated_gt", ms(f.UpdatedAfter))
	}
	for _, id := range f.Assignees {
		v.Add("assignees[]", strconv.Itoa(id))
	}
	return v
}

// HTTPClient talks to the real ClickUp API.
type HTTPClient struct {
	BaseURL  string
	Token    string
	TeamID   string
	HTTP     *http.Client
	Log      *slog.Logger
	CacheTTL time.Duration
	MaxWait  time.Duration // longest we will sleep for a rate limit
	MaxPages int
	Dates    Dates // turns due dates into deadlines; zero value leaves them as stored

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	expires time.Time
	value   any
}

// NewHTTPClient returns a client with a 60 second cache.
func NewHTTPClient(baseURL, token, teamID string, log *slog.Logger) *HTTPClient {
	return &HTTPClient{
		BaseURL:  baseURL,
		Token:    token,
		TeamID:   teamID,
		HTTP:     &http.Client{Timeout: 20 * time.Second},
		Log:      log,
		CacheTTL: 60 * time.Second,
		MaxWait:  60 * time.Second,
		MaxPages: 50,
		cache:    map[string]cacheEntry{},
	}
}

// Tasks returns all tasks matching f, following pagination to the last page.
// Due date filters apply to the normalised deadlines (see Dates).
func (c *HTTPClient) Tasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	query := c.Dates.widen(f).values()
	key := "tasks?" + query.Encode()
	if v, ok := c.cached(key); ok {
		return v.([]Task), nil
	}

	var all []Task
	for page := 0; page < c.MaxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		var resp tasksResponse
		if err := c.get(ctx, fmt.Sprintf("/team/%s/task", url.PathEscape(c.TeamID)), query, &resp); err != nil {
			return nil, err
		}
		for _, at := range resp.Tasks {
			t := at.task()
			c.Dates.Normalise(&t)
			if c.Dates.keep(t, f) {
				all = append(all, t)
			}
		}
		if string(resp.LastPage) == "true" || len(resp.Tasks) == 0 {
			break
		}
		if page == c.MaxPages-1 {
			c.Log.Warn("clickup: stopped paging at the page limit", "pages", c.MaxPages)
		}
	}
	c.store(key, all)
	return all, nil
}

// Members returns the members of the configured workspace.
func (c *HTTPClient) Members(ctx context.Context) ([]Member, error) {
	const key = "members"
	if v, ok := c.cached(key); ok {
		return v.([]Member), nil
	}
	var resp teamsResponse
	if err := c.get(ctx, "/team", nil, &resp); err != nil {
		return nil, err
	}
	for _, team := range resp.Teams {
		if team.ID != c.TeamID {
			continue
		}
		members := make([]Member, 0, len(team.Members))
		for _, m := range team.Members {
			members = append(members, m.User.member())
		}
		c.store(key, members)
		return members, nil
	}
	return nil, fmt.Errorf("clickup: workspace %s not found for this token; check CLICKUP_TEAM_ID", c.TeamID)
}

// TaskDetail returns a task's description and comments.
func (c *HTTPClient) TaskDetail(ctx context.Context, taskID string) (TaskDetail, error) {
	var task taskDetailResponse
	q := url.Values{"include_markdown_description": {"true"}}
	if err := c.get(ctx, "/task/"+url.PathEscape(taskID), q, &task); err != nil {
		return TaskDetail{}, err
	}
	var comments commentsResponse
	if err := c.get(ctx, "/task/"+url.PathEscape(taskID)+"/comment", nil, &comments); err != nil {
		return TaskDetail{}, err
	}
	d := TaskDetail{Description: task.MarkdownDescription}
	if d.Description == "" {
		d.Description = task.Description
	}
	// ClickUp lists comments newest first.
	for i := len(comments.Comments) - 1; i >= 0; i-- {
		cm := comments.Comments[i]
		comment := Comment{Author: cm.User.member(), Text: cm.CommentText}
		if cm.Date.t != nil {
			comment.Date = *cm.Date.t
		}
		d.Comments = append(d.Comments, comment)
	}
	return d, nil
}

// ListStatuses returns the statuses of a list.
func (c *HTTPClient) ListStatuses(ctx context.Context, listID string) ([]Status, error) {
	key := "statuses/" + listID
	if v, ok := c.cached(key); ok {
		return v.([]Status), nil
	}
	var resp listResponse
	if err := c.get(ctx, "/list/"+url.PathEscape(listID), nil, &resp); err != nil {
		return nil, err
	}
	c.store(key, resp.Statuses)
	return resp.Statuses, nil
}

// SetStatus moves a task to status and drops cached task lists.
func (c *HTTPClient) SetStatus(ctx context.Context, taskID, status string) error {
	err := c.send(ctx, http.MethodPut, "/task/"+url.PathEscape(taskID), map[string]any{"status": status})
	c.forgetTasks()
	return err
}

// AddComment posts a comment on a task without notifying anyone.
func (c *HTTPClient) AddComment(ctx context.Context, taskID, text string) error {
	return c.send(ctx, http.MethodPost, "/task/"+url.PathEscape(taskID)+"/comment",
		map[string]any{"comment_text": text, "notify_all": false})
}

// Me returns the user the API token belongs to.
func (c *HTTPClient) Me(ctx context.Context) (Member, error) {
	const key = "me"
	if v, ok := c.cached(key); ok {
		return v.(Member), nil
	}
	var resp userResponse
	if err := c.get(ctx, "/user", nil, &resp); err != nil {
		return Member{}, err
	}
	m := resp.User.member()
	c.store(key, m)
	return m, nil
}

func (c *HTTPClient) forgetTasks() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.cache {
		if strings.HasPrefix(k, "tasks?") {
			delete(c.cache, k)
		}
	}
}

func (c *HTTPClient) cached(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.value, true
}

func (c *HTTPClient) store(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, e := range c.cache {
		if now.After(e.expires) {
			delete(c.cache, k)
		}
	}
	c.cache[key] = cacheEntry{expires: now.Add(c.CacheTTL), value: value}
}

// APIError is a non-2xx response from ClickUp.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("clickup: http %d: %s", e.Status, e.Message)
}

var errRateLimited = errors.New("clickup: rate limited")

func (c *HTTPClient) get(ctx context.Context, path string, query url.Values, out any) error {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return c.request(ctx, http.MethodGet, u, nil, out)
}

// send makes a write request with a JSON body.
func (c *HTTPClient) send(ctx context.Context, method, path string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var out json.RawMessage
	return c.request(ctx, method, c.BaseURL+path, b, &out)
}

func (c *HTTPClient) request(ctx context.Context, method, u string, body []byte, out any) error {
	const attempts = 3
	for attempt := 1; ; attempt++ {
		wait, err := c.do(ctx, method, u, body, out)
		if !errors.Is(err, errRateLimited) {
			return err
		}
		if attempt == attempts || wait > c.MaxWait {
			return fmt.Errorf("clickup: rate limited, try again in %s", wait.Round(time.Second))
		}
		c.Log.Warn("clickup: rate limited, waiting", "wait", wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// do performs one request. On 429 it returns errRateLimited and how long to
// wait; ClickUp rejects rate-limited requests before applying them, so
// retrying a write after a 429 is safe.
func (c *HTTPClient) do(ctx context.Context, method, u string, body []byte, out any) (time.Duration, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", c.Token) // personal tokens use no "Bearer" prefix
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("clickup request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		_, _ = io.Copy(io.Discard, resp.Body)
		return retryAfter(resp.Header, time.Now()), errRateLimited
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		var e struct {
			Err  string `json:"err"`
			Code string `json:"ECODE"`
		}
		msg := http.StatusText(resp.StatusCode)
		if json.Unmarshal(body, &e) == nil && e.Err != "" {
			msg = e.Err + " (" + e.Code + ")"
		}
		return 0, &APIError{Status: resp.StatusCode, Message: msg}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return 0, fmt.Errorf("clickup: decode response: %w", err)
	}
	return 0, nil
}

// retryAfter reads Retry-After (seconds) or X-RateLimit-Reset (Unix seconds).
func retryAfter(h http.Header, now time.Time) time.Duration {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		if d := time.Unix(reset, 0).Sub(now); d > 0 {
			return d + time.Second
		}
		return time.Second
	}
	return 5 * time.Second
}
