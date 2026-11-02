// Package drive provides a folder-bound Drive client. It has no file delete API.
package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// AuthorisedRoot is the only Drive folder authorised for the approval workflow.
const AuthorisedRoot = "10s2NdCuhBiFBXsWa_BGgCxC9gxTElWV0"
const FolderType = "application/vnd.google-apps.folder"

var ErrOutsideRoot = errors.New("file is outside the authorised approval folder")

type File struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mimeType"`
	Parents      []string `json:"parents"`
	Trashed      bool     `json:"trashed"`
	Size         string   `json:"size"`
	ModifiedTime string   `json:"modifiedTime"`
	Description  string   `json:"description"`
}

const fileFields = "id,name,mimeType,parents,trashed,size,modifiedTime,description"

type Client struct {
	HTTP *http.Client
	Base string
	Root string
}

// Open connects using the downloaded web-client credentials and a private token.
func Open(ctx context.Context, credentialsPath, tokenPath string) (*Client, error) {
	data, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("read Drive credentials: %w", err)
	}
	cfg, err := google.ConfigFromJSON(data, "https://www.googleapis.com/auth/drive")
	if err != nil {
		return nil, errors.New("invalid Drive OAuth credentials")
	}
	data, err = os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read Drive token: %w", err)
	}
	var token oauth2.Token
	if json.Unmarshal(data, &token) != nil || token.RefreshToken == "" {
		return nil, errors.New("Drive token has no offline access; reconnect Google")
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 35 * time.Second})
	source := &persistSource{source: cfg.TokenSource(ctx, &token), path: tokenPath, last: token.AccessToken}
	c := &Client{HTTP: oauth2.NewClient(ctx, source), Base: "https://www.googleapis.com", Root: AuthorisedRoot}
	c.HTTP.Timeout = 0 // JSON calls have per-request deadlines; video streams may run longer.
	if _, err := c.Folder(ctx, c.Root); err != nil {
		return nil, fmt.Errorf("check approval root: %w", err)
	}
	if err := c.private(ctx, c.Root); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) private(ctx context.Context, id string) error {
	var list struct {
		Permissions []struct {
			Type string `json:"type"`
		} `json:"permissions"`
	}
	if err := c.request(ctx, http.MethodGet, "/drive/v3/files/"+url.PathEscape(id)+"/permissions", url.Values{"fields": {"permissions(type)"}}, "", nil, &list); err != nil {
		return err
	}
	for _, p := range list.Permissions {
		if p.Type == "anyone" {
			return errors.New("approval records cannot be stored in a publicly shared folder or file")
		}
	}
	return nil
}

type persistSource struct {
	mu         sync.Mutex
	source     oauth2.TokenSource
	path, last string
}

func (s *persistSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.source.Token()
	if err != nil {
		return nil, errors.New("Google authorisation expired or could not refresh; reconnect Drive")
	}
	if token.AccessToken != s.last {
		data, err := json.Marshal(token)
		if err != nil {
			return nil, err
		}
		f, err := os.CreateTemp(filepath.Dir(s.path), ".drive-token-*")
		if err != nil {
			return nil, errors.New("cannot update private Drive token file")
		}
		name := f.Name()
		defer os.Remove(name)
		if _, err = f.Write(data); err != nil {
			_ = f.Close()
			return nil, errors.New("cannot save refreshed Drive token")
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
		if err = os.Rename(name, s.path); err != nil {
			return nil, errors.New("cannot replace private Drive token file")
		}
		s.last = token.AccessToken
	}
	return token, nil
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, contentType string, body io.Reader, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if query == nil {
		query = url.Values{}
	}
	query.Set("supportsAllDrives", "true")
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path+"?"+query.Encode(), body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("Drive request could not complete")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("Drive returned HTTP %d", resp.StatusCode)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func (c *Client) rawFile(ctx context.Context, id string) (File, error) {
	var f File
	if id == "" || strings.ContainsAny(id, "/?\\") {
		return f, ErrOutsideRoot
	}
	err := c.request(ctx, http.MethodGet, "/drive/v3/files/"+url.PathEscape(id), url.Values{"fields": {fileFields}}, "", nil, &f)
	return f, err
}

// File verifies ancestry on every call, including before every write or stream.
func (c *Client) File(ctx context.Context, id string) (File, error) {
	f, err := c.rawFile(ctx, id)
	if err != nil {
		return f, err
	}
	current := f
	seen := map[string]bool{}
	for depth := 0; depth < 40; depth++ {
		if current.Trashed || current.MimeType == "application/vnd.google-apps.shortcut" || seen[current.ID] {
			return File{}, ErrOutsideRoot
		}
		if current.ID == c.Root {
			return f, nil
		}
		seen[current.ID] = true
		if len(current.Parents) != 1 {
			return File{}, ErrOutsideRoot
		}
		// The Shared Drive root is not a permitted ancestor to walk into.
		current, err = c.rawFile(ctx, current.Parents[0])
		if err != nil {
			return File{}, ErrOutsideRoot
		}
		if current.MimeType != FolderType {
			return File{}, ErrOutsideRoot
		}
	}
	return File{}, ErrOutsideRoot
}

func (c *Client) Folder(ctx context.Context, id string) (File, error) {
	f, err := c.File(ctx, id)
	if err == nil && f.MimeType != FolderType {
		err = errors.New("expected an approval folder")
	}
	return f, err
}

func (c *Client) Children(ctx context.Context, parent string) ([]File, error) {
	if _, err := c.Folder(ctx, parent); err != nil {
		return nil, err
	}
	var files []File
	q := url.Values{"q": {"'" + strings.ReplaceAll(parent, "'", "\\'") + "' in parents and trashed = false"}, "includeItemsFromAllDrives": {"true"}, "pageSize": {"100"}, "fields": {"nextPageToken,files(" + fileFields + ")"}}
	for {
		var page struct {
			Files []File `json:"files"`
			Next  string `json:"nextPageToken"`
		}
		if err := c.request(ctx, http.MethodGet, "/drive/v3/files", q, "", nil, &page); err != nil {
			return nil, err
		}
		for _, f := range page.Files {
			if f.MimeType != "application/vnd.google-apps.shortcut" {
				files = append(files, f)
			}
		}
		if page.Next == "" {
			return files, nil
		}
		q.Set("pageToken", page.Next)
	}
}

func (c *Client) EnsureFolder(ctx context.Context, parent, name string) (File, error) {
	children, err := c.Children(ctx, parent)
	if err != nil {
		return File{}, err
	}
	var found []File
	for _, f := range children {
		if f.Name == name && f.MimeType == FolderType {
			found = append(found, f)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	if len(found) > 1 {
		return File{}, errors.New("duplicate approval folder names; resolve before continuing")
	}
	data, _ := json.Marshal(map[string]any{"name": name, "mimeType": FolderType, "parents": []string{parent}})
	var f File
	err = c.request(ctx, http.MethodPost, "/drive/v3/files", url.Values{"fields": {fileFields}}, "application/json", bytes.NewReader(data), &f)
	return f, err
}

func (c *Client) CreateJSON(ctx context.Context, parent, name string, value any) (File, error) {
	if _, err := c.Folder(ctx, parent); err != nil {
		return File{}, err
	}
	if err := c.private(ctx, parent); err != nil {
		return File{}, err
	}
	metadata, _ := json.Marshal(map[string]any{"name": name, "mimeType": "application/json", "parents": []string{parent}})
	content, err := json.Marshal(value)
	if err != nil {
		return File{}, err
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, part := range []struct {
		kind string
		data []byte
	}{{"application/json; charset=UTF-8", metadata}, {"application/json", content}} {
		p, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {part.kind}})
		if err != nil {
			return File{}, err
		}
		if _, err := p.Write(part.data); err != nil {
			return File{}, err
		}
	}
	if err := w.Close(); err != nil {
		return File{}, err
	}
	var f File
	err = c.request(ctx, http.MethodPost, "/upload/drive/v3/files", url.Values{"uploadType": {"multipart"}, "fields": {fileFields}}, "multipart/related; boundary="+w.Boundary(), &body, &f)
	return f, err
}

func (c *Client) ReadJSON(ctx context.Context, id string, out any) error {
	f, err := c.File(ctx, id)
	if err != nil {
		return err
	}
	if f.MimeType != "application/json" {
		return errors.New("approval record must be JSON")
	}
	if err := c.private(ctx, id); err != nil {
		return err
	}
	return c.request(ctx, http.MethodGet, "/drive/v3/files/"+id, url.Values{"alt": {"media"}}, "", nil, out)
}

func (c *Client) WriteJSON(ctx context.Context, id string, value any) error {
	f, err := c.File(ctx, id)
	if err != nil {
		return err
	}
	if f.MimeType != "application/json" || id == c.Root {
		return errors.New("refusing to overwrite non-record Drive content")
	}
	if err := c.private(ctx, id); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.request(ctx, http.MethodPatch, "/upload/drive/v3/files/"+id, url.Values{"uploadType": {"media"}}, "application/json", bytes.NewReader(data), nil)
}

func (c *Client) Move(ctx context.Context, id, destination, name, description string) error {
	f, err := c.File(ctx, id)
	if err != nil {
		return err
	}
	if id == c.Root || f.MimeType == FolderType {
		return errors.New("refusing to move an approval folder")
	}
	if _, err := c.Folder(ctx, destination); err != nil {
		return err
	}
	q := url.Values{"fields": {"id"}}
	if len(f.Parents) != 1 {
		return ErrOutsideRoot
	}
	if f.Parents[0] != destination {
		q.Set("removeParents", f.Parents[0])
		q.Set("addParents", destination)
	}
	data, _ := json.Marshal(map[string]string{"name": name, "description": description})
	return c.request(ctx, http.MethodPatch, "/drive/v3/files/"+id, q, "application/json", bytes.NewReader(data), nil)
}

// Stream forwards only video bytes from a freshly verified in-root file. Range
// requests allow the browser scrubber to seek without downloading the whole file.
func (c *Client) Stream(ctx context.Context, id string, w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	f, err := c.File(ctx, id)
	if err != nil || !strings.HasPrefix(f.MimeType, "video/") {
		http.NotFound(w, r)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/drive/v3/files/"+url.PathEscape(id)+"?alt=media&supportsAllDrives=true", nil)
	if err != nil {
		http.Error(w, "Unable to load preview", http.StatusBadGateway)
		return
	}
	for _, header := range []string{"Range", "If-Range"} {
		if value := r.Header.Get(header); value != "" {
			req.Header.Set(header, value)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		http.Error(w, "Unable to load preview", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		http.Error(w, "Preview unavailable", http.StatusBadGateway)
		return
	}
	for _, header := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}
