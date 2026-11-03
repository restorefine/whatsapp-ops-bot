package editorcounts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testWeb(t *testing.T) (*Web, *Service, *time.Time) {
	t.Helper()
	s, _, _, now := fixture()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "editor-counts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "editor-counts", "index.html"), []byte("admin-report-shell"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := NewWeb(s, []byte(strings.Repeat("k", 32)), "https://example.test", dir, s.Admins)
	if err != nil {
		t.Fatal(err)
	}
	w.Now = s.Now
	return w, s, now
}

func TestAdminLinkPreviewsDoNotConsumeAndSessionIsSeparate(t *testing.T) {
	w, s, _ := testWeb(t)
	if _, err := w.Link("editor-number"); err == nil {
		t.Fatal("editor obtained admin link")
	}
	link, err := w.Link(s.Admins[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD", "GET"} {
		rw := httptest.NewRecorder()
		w.ServeHTTP(rw, httptest.NewRequest(method, link, nil))
		if rw.Code != 200 {
			t.Fatal("preview consumed link")
		}
	}
	rw := httptest.NewRecorder()
	req := httptest.NewRequest("POST", link, nil)
	req.Header.Set("Origin", "https://evil.test")
	w.ServeHTTP(rw, req)
	if rw.Code != 403 {
		t.Fatal("cross-origin redemption allowed")
	}
	rw = httptest.NewRecorder()
	req = httptest.NewRequest("POST", link, nil)
	req.Header.Set("Origin", w.Origin)
	w.ServeHTTP(rw, req)
	if rw.Code != http.StatusSeeOther {
		t.Fatalf("redemption: %d %s", rw.Code, rw.Body.String())
	}
	cookies := rw.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("unsafe admin session")
	}
	for _, path := range []string{"/admin/editors/", "/api/admin/editors", "/api/admin/editors.xlsx"} {
		rw = httptest.NewRecorder()
		req = httptest.NewRequest("GET", w.Origin+path, nil)
		req.AddCookie(cookies[0])
		w.ServeHTTP(rw, req)
		if rw.Code != 200 || rw.Header().Get("Cache-Control") != "no-store, private" {
			t.Fatalf("protected path %s: %d", path, rw.Code)
		}
		if strings.HasSuffix(path, "xlsx") && !strings.HasPrefix(rw.Body.String(), "PK") {
			t.Fatal("not an XLSX ZIP")
		}
	}
	rw = httptest.NewRecorder()
	w.ServeHTTP(rw, httptest.NewRequest("GET", link, nil))
	if rw.Code != 401 {
		t.Fatal("single-use link reused")
	}
	for _, path := range []string{"/api/admin/editors", "/api/admin/editors.xlsx", "/admin/editors/"} {
		rw = httptest.NewRecorder()
		req = httptest.NewRequest("GET", w.Origin+path, nil)
		req.AddCookie(&http.Cookie{Name: "rr_review", Value: cookies[0].Value})
		w.ServeHTTP(rw, req)
		if rw.Code != 401 {
			t.Fatal("client cookie opened admin report")
		}
	}
}

func TestExpiredLinkAndAdminRemovalRejectSession(t *testing.T) {
	w, s, now := testWeb(t)
	link, _ := w.Link(s.Admins[0])
	*now = now.Add(16 * time.Minute)
	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, httptest.NewRequest("GET", link, nil))
	if rw.Code != 401 {
		t.Fatal("expired link accepted")
	}
	link, _ = w.Link(s.Admins[0])
	rw = httptest.NewRecorder()
	req := httptest.NewRequest("POST", link, nil)
	req.Header.Set("Origin", w.Origin)
	w.ServeHTTP(rw, req)
	cookie := rw.Result().Cookies()[0]
	delete(w.Admins, s.Admins[0])
	req = httptest.NewRequest("GET", w.Origin+"/api/admin/editors", nil)
	req.AddCookie(cookie)
	if w.Authenticated(req) {
		t.Fatal("removed admin retained access")
	}
	w.Admins[s.Admins[0]] = true
	*now = now.Add(9 * time.Hour)
	if w.Authenticated(req) {
		t.Fatal("expired session accepted")
	}
}

func TestWorkbookIsSafeAndContainsAllEvidence(t *testing.T) {
	s, cu, _, now := fixture()
	_, _ = s.Report(context.Background())
	*now = now.Add(2 * time.Minute)
	cu.tasks = append(cu.tasks, completed("proof", 1, "Arpan", *now))
	cu.tasks[0].Name = "=HYPERLINK(\"bad\") < & \x00"
	r, err := s.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := Workbook(r)
	if err != nil {
		t.Fatal(err)
	}
	checkWorkbook(t, data)
}
