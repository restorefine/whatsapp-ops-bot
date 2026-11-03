package editorcounts

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const adminCookie = "rr_editor_admin"

type grant struct {
	Number  string
	Expires time.Time
}

type Web struct {
	Service           *Service
	Origin, StaticDir string
	Key               []byte
	Admins            map[string]bool
	Now               func() time.Time
	mu                sync.Mutex
	pending           map[[32]byte]grant
}

func NewWeb(s *Service, key []byte, origin, staticDir string, admins []string) (*Web, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return nil, fmt.Errorf("invalid editor report origin")
	}
	if len(key) < 32 {
		return nil, fmt.Errorf("editor report needs the persistent signing key")
	}
	if info, err := os.Stat(filepath.Join(staticDir, "editor-counts", "index.html")); err != nil || info.IsDir() {
		return nil, fmt.Errorf("rebuild the portal to include the editor report")
	}
	w := &Web{Service: s, Key: append([]byte(nil), key...), Origin: origin, StaticDir: staticDir, Admins: map[string]bool{}, Now: time.Now, pending: map[[32]byte]grant{}}
	for _, n := range admins {
		w.Admins[n] = true
	}
	return w, nil
}

// Link is called only after the command router has checked the sender's role.
// GET previews never consume a grant; the admin must press Open report.
func (w *Web) Link(number string) (string, error) {
	if !w.Admins[number] {
		return "", fmt.Errorf("editor reports are admin-only")
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, g := range w.pending {
		if !g.Expires.After(w.Now()) || g.Number == number {
			delete(w.pending, k)
		}
	}
	w.pending[sha256.Sum256([]byte(token))] = grant{Number: number, Expires: w.Now().Add(15 * time.Minute)}
	return w.Origin + "/admin/editors/access/" + token, nil
}

func (w *Web) signature(value string) string {
	h := hmac.New(sha256.New, w.Key)
	_, _ = h.Write([]byte("editor-admin-session\x00" + value))
	return hex.EncodeToString(h.Sum(nil))
}

func (w *Web) Authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(adminCookie)
	if err != nil {
		return false
	}
	p := strings.Split(cookie.Value, ".")
	if len(p) != 3 || !w.Admins[p[0]] {
		return false
	}
	exp, err := strconv.ParseInt(p[1], 10, 64)
	return err == nil && exp > w.Now().Unix() && hmac.Equal([]byte(p[2]), []byte(w.signature(p[0]+"."+p[1])))
}

func (w *Web) Handles(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/admin/editors") || strings.HasPrefix(r.URL.Path, "/api/admin/editors") || strings.HasPrefix(r.URL.Path, "/portal/editor-counts")
}

func (w *Web) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	secureHeaders(rw)
	if strings.HasPrefix(r.URL.Path, "/admin/editors/access/") {
		w.access(rw, r)
		return
	}
	if !w.Authenticated(r) {
		http.Error(rw, "Admin access required. Send /editors to the bot from your admin WhatsApp number.", http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/admin/editors" && r.Method == http.MethodGet:
		http.Redirect(rw, r, "/admin/editors/", http.StatusSeeOther)
	case (r.URL.Path == "/admin/editors/" || r.URL.Path == "/portal/editor-counts/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		http.ServeFile(rw, r, filepath.Join(w.StaticDir, "editor-counts", "index.html"))
	case (r.URL.Path == "/api/admin/editors" || r.URL.Path == "/api/admin/editors.xlsx") && r.Method == http.MethodGet:
		_ = http.NewResponseController(rw).SetWriteDeadline(time.Now().Add(2 * time.Minute))
		report, err := w.Service.Report(r.Context())
		if err != nil {
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(rw).Encode(map[string]string{"error": "Could not verify the full ClickUp count. Saved audit history has been retained. Try again shortly."})
			return
		}
		if r.URL.Path == "/api/admin/editors.xlsx" {
			data, err := Workbook(report)
			if err != nil {
				http.Error(rw, "Could not create workbook", http.StatusInternalServerError)
				return
			}
			rw.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
			rw.Header().Set("Content-Disposition", `attachment; filename="editor-video-counts-`+report.CheckedAt.Format("2006-01-02")+`.xlsx"`)
			_, _ = rw.Write(data)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(report)
	default:
		http.NotFound(rw, r)
	}
}

func secureHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
}

var accessPage = template.Must(template.New("access").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Editor video counts · RestoRefine</title><style>body{font:18px/1.6 system-ui;margin:10vh auto;padding:24px;max-width:560px;color:#131315}h1{font-size:36px;letter-spacing:-1px}button{font:inherit;background:#131315;color:white;padding:14px 28px;border:0;border-radius:8px;cursor:pointer}p{color:#666}</style><h1>Editor video counts</h1><p>Open your admin report for the video team. This link expires in 15 minutes and can be used once. Keep it private.</p><form method="post"><button type="submit">Open report</button></form></html>`))

func (w *Web) access(rw http.ResponseWriter, r *http.Request) {
	// Chromium serialises a form POST's Origin as null under no-referrer.
	// Keep the Origin check usable while suppressing referrers to other sites.
	rw.Header().Set("Referrer-Policy", "same-origin")
	token := strings.TrimPrefix(r.URL.Path, "/admin/editors/access/")
	if len(token) != 64 {
		http.NotFound(rw, r)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	k := sha256.Sum256([]byte(token))
	g, ok := w.pending[k]
	if !ok || !g.Expires.After(w.Now()) || !w.Admins[g.Number] {
		delete(w.pending, k)
		http.Error(rw, "This link expired or was already used. Send /editors to the bot for a fresh admin link.", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodGet {
			_ = accessPage.Execute(rw, nil)
		}
		return
	}
	if r.Method != http.MethodPost {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Origin") != w.Origin {
		http.Error(rw, "Open this link directly in your browser.", http.StatusForbidden)
		return
	}
	delete(w.pending, k)
	expires := w.Now().Add(8 * time.Hour)
	body := g.Number + "." + strconv.FormatInt(expires.Unix(), 10)
	http.SetCookie(rw, &http.Cookie{Name: adminCookie, Value: body + "." + w.signature(body), Path: "/", HttpOnly: true, Secure: strings.HasPrefix(w.Origin, "https://"), SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: 8 * 60 * 60})
	http.Redirect(rw, r, "/admin/editors/", http.StatusSeeOther)
}

// Asset serves only the shared static assets, never another exported page.
func (w *Web) Asset(rw http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	allowed := strings.HasPrefix(p, "/portal/_next/static/") || strings.HasPrefix(p, "/portal/fonts/") || p == "/portal/restorefine-logoblack.svg" || p == "/portal/restorefine-logowhite.svg"
	if !allowed || !w.Authenticated(r) {
		return false
	}
	secureHeaders(rw)
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || strings.Contains(strings.TrimPrefix(p, "/portal/"), "..") {
		http.NotFound(rw, r)
		return true
	}
	path := filepath.Join(w.StaticDir, filepath.FromSlash(strings.TrimPrefix(p, "/portal/")))
	if f, err := os.Stat(path); err != nil || f.IsDir() {
		http.NotFound(rw, r)
		return true
	}
	http.ServeFile(rw, r, path)
	return true
}
