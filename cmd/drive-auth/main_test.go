package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"
)

func TestLoadConfigRequiresRegisteredLocalCallback(t *testing.T) {
	for _, tc := range []struct {
		name, redirect string
		wantError      bool
	}{
		{"local callback", redirectURI, false},
		{"different callback", "https://example.com/callback", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "client.json")
			data, err := json.Marshal(map[string]any{"web": map[string]any{
				"client_id": "test-client", "client_secret": "test-secret",
				"redirect_uris": []string{tc.redirect},
				"auth_uri":      "https://accounts.google.com/o/oauth2/auth",
				"token_uri":     "https://oauth2.googleapis.com/token",
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadConfig(path)
			if (err != nil) != tc.wantError {
				t.Fatalf("loadConfig error = %v, wantError = %v", err, tc.wantError)
			}
			if !tc.wantError && cfg.RedirectURL != redirectURI {
				t.Fatal("wrong callback configured")
			}
		})
	}
}

func TestCallbackRequiresBrowserStateAndExchangesOnlyOnce(t *testing.T) {
	exchanges := 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("code_verifier") != "test-verifier" || r.Form.Get("code") != "test-code" {
			t.Error("missing PKCE verifier or authorisation code")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-access","refresh_token":"test-refresh","token_type":"Bearer","expires_in":3600,"scope":"https://www.googleapis.com/auth/drive"}`))
	}))
	defer endpoint.Close()
	output := filepath.Join(t.TempDir(), "private", "token.json")
	s := &authSession{
		cfg: &oauth2.Config{
			ClientID: "test-client", ClientSecret: "test-secret", RedirectURL: redirectURI,
			Endpoint: oauth2.Endpoint{TokenURL: endpoint.URL, AuthStyle: oauth2.AuthStyleInParams},
		},
		state: "test-state", verifier: "test-verifier", output: output,
		done: make(chan error, 1),
	}
	for _, tc := range []struct {
		name, state, cookie string
		wantStatus          int
	}{
		{"wrong state", "wrong", "test-state", http.StatusBadRequest},
		{"no browser cookie", "test-state", "", http.StatusBadRequest},
		{"valid callback", "test-state", "test-state", http.StatusOK},
		{"replayed callback", "test-state", "test-state", http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{"state": {tc.state}, "code": {"test-code"}}
			r := httptest.NewRequest(http.MethodGet, redirectURI+"?"+q.Encode(), nil)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "drive_oauth_state", Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
		})
	}
	if exchanges != 1 {
		t.Fatalf("token exchanged %d times, want once", exchanges)
	}
	if err := <-s.done; err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("token permissions = %o, want 600", info.Mode().Perm())
	}
	if err := saveToken(output, &oauth2.Token{RefreshToken: "replacement"}); err == nil {
		t.Fatal("existing token must not be overwritten")
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var saved oauth2.Token
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.RefreshToken != "test-refresh" {
		t.Fatal("existing token was changed")
	}
}
