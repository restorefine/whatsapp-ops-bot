// Package metricool is a small read-only client for the Metricool API: the
// brands an account manages and the posts each one has published.
package metricool

import (
	"context"
	"encoding/json"
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

// Brand is one Metricool brand (a "blog" in the API).
type Brand struct {
	ID    int
	Label string
}

// Network is one network a post went to and how it went.
type Network struct {
	Name   string // facebook, instagram, tiktok, ...
	Status string // PUBLISHED, PENDING, ERROR, ...
	URL    string
}

// Post is a post on one brand.
type Post struct {
	Time     time.Time
	Text     string
	Draft    bool
	Networks []Network
}

// Published reports whether the post went out on at least one network.
func (p Post) Published() bool {
	for _, n := range p.Networks {
		if n.Status == "PUBLISHED" {
			return true
		}
	}
	return false
}

// Failed reports whether any network rejected the post.
func (p Post) Failed() bool {
	for _, n := range p.Networks {
		if n.Status == "ERROR" || n.Status == "FAILED" {
			return true
		}
	}
	return false
}

// Client reads from Metricool. It never schedules, edits or deletes posts.
type Client struct {
	BaseURL string // https://app.metricool.com/api
	Token   string // REST API token, sent as X-Mc-Auth
	UserID  string
	HTTP    *http.Client
	Log     *slog.Logger

	mu       sync.Mutex
	brands   []Brand
	brandsAt time.Time
}

// New returns a client with a 20 second timeout.
func New(baseURL, token, userID string, log *slog.Logger) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, UserID: userID, HTTP: &http.Client{Timeout: 20 * time.Second}, Log: log}
}

// brandsTTL keeps the brand list for an hour, so a brand added in Metricool
// shows up the same day.
const brandsTTL = time.Hour

// Brands returns the brands the account manages.
func (c *Client) Brands(ctx context.Context) ([]Brand, error) {
	c.mu.Lock()
	if c.brands != nil && time.Since(c.brandsAt) < brandsTTL {
		defer c.mu.Unlock()
		return c.brands, nil
	}
	c.mu.Unlock()

	var resp []struct {
		ID    int    `json:"id"`
		Label string `json:"label"`
	}
	if err := c.get(ctx, "/admin/simpleProfiles", url.Values{"userId": {c.UserID}}, &resp); err != nil {
		return nil, err
	}
	brands := make([]Brand, 0, len(resp))
	for _, b := range resp {
		brands = append(brands, Brand{ID: b.ID, Label: strings.TrimSpace(b.Label)})
	}
	c.mu.Lock()
	c.brands, c.brandsAt = brands, time.Now()
	c.mu.Unlock()
	return brands, nil
}

// Posts returns a brand's posts with a publication time in [from, to), in
// from's time zone.
func (c *Client) Posts(ctx context.Context, brandID int, from, to time.Time) ([]Post, error) {
	const layout = "2006-01-02T15:04:05"
	q := url.Values{
		"blogId":   {strconv.Itoa(brandID)},
		"userId":   {c.UserID},
		"start":    {from.Format(layout)},
		"end":      {to.Add(-time.Second).Format(layout)},
		"timezone": {from.Location().String()},
	}
	var resp struct {
		Data []struct {
			PublicationDate struct {
				DateTime string `json:"dateTime"`
				Timezone string `json:"timezone"`
			} `json:"publicationDate"`
			Text      string `json:"text"`
			Draft     bool   `json:"draft"`
			Providers []struct {
				Network   string `json:"network"`
				Status    string `json:"status"`
				PublicURL string `json:"publicUrl"`
			} `json:"providers"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/v2/scheduler/posts", q, &resp); err != nil {
		return nil, err
	}
	posts := make([]Post, 0, len(resp.Data))
	for _, d := range resp.Data {
		loc := from.Location()
		if l, err := time.LoadLocation(d.PublicationDate.Timezone); err == nil {
			loc = l
		}
		at, err := time.ParseInLocation(layout, d.PublicationDate.DateTime, loc)
		if err != nil {
			c.Log.Warn("metricool: unreadable publication date", "value", d.PublicationDate.DateTime)
			continue
		}
		p := Post{Time: at, Text: d.Text, Draft: d.Draft}
		for _, pr := range d.Providers {
			p.Networks = append(p.Networks, Network{Name: pr.Network, Status: strings.ToUpper(pr.Status), URL: pr.PublicURL})
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Mc-Auth", c.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("metricool request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return fmt.Errorf("metricool: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("metricool: decode response: %w", err)
	}
	return nil
}
