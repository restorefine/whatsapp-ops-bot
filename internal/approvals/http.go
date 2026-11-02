package approvals

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Web serves the exported Next.js frontend and a same-origin, session-bound API.
// OAuth credentials and Drive file IDs cannot be selected by client requests.
type Web struct {
	Service   *Service
	Key       []byte
	Origin    string
	StaticDir string
	Secure    bool
}

func NewWeb(service *Service, key []byte, origin, staticDir string) (*Web, error) {
	if len(key) < 32 {
		return nil, errors.New("portal signing key must contain at least 32 bytes")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("portal origin must be an HTTP(S) origin without a path")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		return nil, errors.New("use HTTPS for the live portal")
	}
	if staticDir != "" {
		info, err := os.Stat(filepath.Join(staticDir, "index.html"))
		if err != nil || info.IsDir() {
			return nil, errors.New("build the Next.js portal first: cd portal && npm run build")
		}
	}
	return &Web{Service: service, Key: key, Origin: origin, StaticDir: staticDir, Secure: u.Scheme == "https"}, nil
}

func (w *Web) sign(purpose, value string) string {
	mac := hmac.New(sha256.New, w.Key)
	_, _ = mac.Write([]byte(purpose + "\x00" + value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (w *Web) Link(client Client) string {
	body := client.ID + "." + strconv.Itoa(client.Generation)
	return w.Origin + "/c/" + body + "." + w.sign("link", body)
}

func (w *Web) linkClient(token string) (Client, bool) {
	p := strings.Split(token, ".")
	if len(p) != 3 {
		return Client{}, false
	}
	body := p[0] + "." + p[1]
	if !hmac.Equal([]byte(p[2]), []byte(w.sign("link", body))) {
		return Client{}, false
	}
	c, ok := w.Service.Client(p[0])
	return c, ok && p[1] == strconv.Itoa(c.Generation)
}

func (w *Web) session(r *http.Request) (Client, string, bool) {
	cookie, err := r.Cookie("rr_review")
	if err != nil {
		return Client{}, "", false
	}
	p := strings.Split(cookie.Value, ".")
	if len(p) != 4 {
		return Client{}, "", false
	}
	body := strings.Join(p[:3], ".")
	if !hmac.Equal([]byte(p[3]), []byte(w.sign("session", body))) {
		return Client{}, "", false
	}
	expires, err := strconv.ParseInt(p[2], 10, 64)
	if err != nil || time.Now().Unix() >= expires {
		return Client{}, "", false
	}
	client, ok := w.Service.Client(p[0])
	if !ok || strconv.Itoa(client.Generation) != p[1] {
		return Client{}, "", false
	}
	return client, w.sign("csrf", cookie.Value), true
}

func (w *Web) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Cache-Control", "private, no-store")
	rw.Header().Set("Referrer-Policy", "no-referrer")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	rw.Header().Set("X-Frame-Options", "DENY")
	rw.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if strings.HasPrefix(r.URL.Path, "/c/") {
		if r.Method != http.MethodGet {
			http.NotFound(rw, r)
			return
		}
		client, ok := w.linkClient(strings.TrimPrefix(r.URL.Path, "/c/"))
		if !ok {
			http.Error(rw, "This review link is invalid or has been replaced.", http.StatusUnauthorized)
			return
		}
		expires := time.Now().Add(30 * 24 * time.Hour)
		body := fmt.Sprintf("%s.%d.%d", client.ID, client.Generation, expires.Unix())
		http.SetCookie(rw, &http.Cookie{Name: "rr_review", Value: body + "." + w.sign("session", body), Path: "/", HttpOnly: true, Secure: w.Secure, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 60 * 60, Expires: expires})
		http.Redirect(rw, r, "/portal/", http.StatusSeeOther)
		return
	}
	client, csrf, ok := w.session(r)
	if !ok {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(rw, http.StatusUnauthorized, map[string]string{"error": "Open your private review link to access these videos."})
		} else {
			http.Error(rw, "Open the private review link sent to you on WhatsApp.", http.StatusUnauthorized)
		}
		return
	}
	switch {
	case r.URL.Path == "/api/portal" && r.Method == http.MethodGet:
		writeJSON(rw, http.StatusOK, w.workspace(client, csrf))
	case r.URL.Path == "/api/portal/action" && r.Method == http.MethodPost:
		if r.Header.Get("Origin") != w.Origin || !hmac.Equal([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrf)) {
			writeJSON(rw, http.StatusForbidden, map[string]string{"error": "Refresh the page before sending feedback."})
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			writeJSON(rw, http.StatusUnsupportedMediaType, map[string]string{"error": "Use a JSON feedback request."})
			return
		}
		var action Action
		decoder := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 24<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&action) != nil || decoder.Decode(new(any)) != io.EOF {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "Invalid feedback request."})
			return
		}
		if err := w.Service.Act(r.Context(), client.ID, action); err != nil {
			writeJSON(rw, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(rw, http.StatusOK, w.workspace(client, csrf))
	case strings.HasPrefix(r.URL.Path, "/api/portal/video/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		// Video responses can outlive the normal webhook response deadline.
		_ = http.NewResponseController(rw).SetWriteDeadline(time.Now().Add(5 * time.Minute))
		w.Service.Stream(r.Context(), client.ID, strings.TrimPrefix(r.URL.Path, "/api/portal/video/"), rw, r)
	case (r.URL.Path == "/portal" || strings.HasPrefix(r.URL.Path, "/portal/")) && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		if w.StaticDir == "" {
			http.NotFound(rw, r)
			return
		}
		// Exported files contain no client data. APIs and the page itself still
		// require the session so that forwarded asset URLs expose nothing useful.
		rw.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; media-src 'self' blob:; connect-src 'self'; frame-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if r.URL.Path == "/portal" {
			http.Redirect(rw, r, "/portal/", http.StatusPermanentRedirect)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/portal/")
		if path == "" {
			path = "index.html"
		}
		if strings.Contains(path, "..") || strings.HasPrefix(path, "/") {
			http.NotFound(rw, r)
			return
		}
		// No directory listings or Next's source/build metadata are served.
		f, err := os.Stat(filepath.Join(w.StaticDir, filepath.FromSlash(path)))
		if err != nil || f.IsDir() {
			http.NotFound(rw, r)
			return
		}
		http.ServeFile(rw, r, filepath.Join(w.StaticDir, filepath.FromSlash(path)))
	default:
		http.NotFound(rw, r)
	}
}

func (w *Web) workspace(client Client, csrf string) any {
	type publicReview struct {
		ID          string     `json:"id"`
		Title       string     `json:"title"`
		PostingDate string     `json:"postingDate"`
		Version     int        `json:"version"`
		Status      string     `json:"status"`
		CreatedAt   time.Time  `json:"createdAt"`
		DecidedAt   *time.Time `json:"decidedAt,omitempty"`
		Notes       []Note     `json:"notes"`
		PreviewURL  string     `json:"previewUrl"`
	}
	reviews := []publicReview{}
	for _, r := range w.Service.Reviews(client.ID) {
		reviews = append(reviews, publicReview{ID: r.ID, Title: r.Title, PostingDate: r.PostingDate, Version: r.Version, Status: r.Status, CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt, Notes: r.Notes, PreviewURL: "/api/portal/video/" + url.PathEscape(r.ID)})
	}
	return struct {
		Client  any            `json:"client"`
		Reviews []publicReview `json:"reviews"`
		CSRF    string         `json:"csrf"`
		Demo    bool           `json:"demo"`
	}{Client: struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Initials string `json:"initials"`
	}{client.ID, client.Name, client.Initials}, Reviews: reviews, CSRF: csrf}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
