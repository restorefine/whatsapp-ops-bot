package drive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRootBoundaryPreventsOutsideWritesAndStreams(t *testing.T) {
	files := map[string]File{
		"root":      {ID: "root", MimeType: FolderType},
		"client":    {ID: "client", MimeType: FolderType, Parents: []string{"root"}},
		"video":     {ID: "video", MimeType: "video/mp4", Parents: []string{"client"}},
		"outside":   {ID: "outside", MimeType: FolderType},
		"old-video": {ID: "old-video", MimeType: "video/mp4", Parents: []string{"outside"}},
		"old-json":  {ID: "old-json", MimeType: "application/json", Parents: []string{"outside"}},
		"shortcut":  {ID: "shortcut", MimeType: "application/vnd.google-apps.shortcut", Parents: []string{"root"}},
	}
	writes, streams := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
			w.WriteHeader(200)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")
		if r.URL.Query().Get("alt") == "media" {
			streams++
			if r.Header.Get("Range") != "bytes=2-4" {
				t.Error("Range header was not forwarded")
			}
			w.Header().Set("Content-Range", "bytes 2-4/10")
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("234"))
			return
		}
		file, ok := files[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(file)
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), Base: server.URL, Root: "root"}
	ctx := context.Background()
	for _, id := range []string{"old-video", "old-json", "shortcut"} {
		if _, err := c.File(ctx, id); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("%s: want outside-root error, got %v", id, err)
		}
	}
	if err := c.Move(ctx, "old-video", "client", "new name", ""); err == nil {
		t.Fatal("outside source accepted")
	}
	if err := c.Move(ctx, "video", "outside", "new name", ""); err == nil {
		t.Fatal("outside destination accepted")
	}
	if err := c.WriteJSON(ctx, "old-json", map[string]string{"bad": "write"}); err == nil {
		t.Fatal("outside record accepted")
	}
	bad := httptest.NewRecorder()
	c.Stream(ctx, "old-video", bad, httptest.NewRequest(http.MethodGet, "/", nil))
	if bad.Code != 404 {
		t.Fatal("outside video streamed")
	}
	if writes != 0 || streams != 0 {
		t.Fatalf("outside operations reached a mutation or media request: writes=%d streams=%d", writes, streams)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Range", "bytes=2-4")
	good := httptest.NewRecorder()
	c.Stream(ctx, "video", good, request)
	if good.Code != 206 || good.Body.String() != "234" || good.Header().Get("Content-Range") != "bytes 2-4/10" {
		t.Fatalf("seek response: %d %q %+v", good.Code, good.Body.String(), good.Header())
	}
}
