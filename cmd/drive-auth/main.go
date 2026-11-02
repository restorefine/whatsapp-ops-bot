// Command drive-auth connects a Google account to Drive using a local browser.
// It does not start the bot, modify Drive, or send WhatsApp messages.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	redirectURI = "http://localhost:8091/oauth/google/callback"
	driveScope  = "https://www.googleapis.com/auth/drive"
)

func main() {
	credentials := flag.String("credentials", "", "path to the downloaded Google OAuth web-client JSON")
	output := flag.String("output", ".secrets/drive-token.json", "new private file for the Google token (must not already exist)")
	flag.Parse()
	if *credentials == "" {
		fmt.Fprintln(os.Stderr, "Usage: drive-auth -credentials /path/to/client_secret.json [-output .secrets/drive-token.json]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *credentials, *output); err != nil {
		fmt.Fprintln(os.Stderr, "Drive connection:", err)
		os.Exit(1)
	}
}

func loadConfig(path string) (*oauth2.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var downloaded struct {
		Web struct {
			RedirectURIs []string `json:"redirect_uris"`
		} `json:"web"`
	}
	if json.Unmarshal(data, &downloaded) != nil {
		return nil, errors.New("credentials are not valid JSON")
	}
	registered := false
	for _, uri := range downloaded.Web.RedirectURIs {
		registered = registered || uri == redirectURI
	}
	if !registered {
		return nil, fmt.Errorf("use web-client credentials with the registered redirect URI %s", redirectURI)
	}
	cfg, err := google.ConfigFromJSON(data, driveScope)
	if err != nil || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("credentials must contain a Google OAuth client ID and client secret")
	}
	cfg.RedirectURL = redirectURI
	return cfg, nil
}

func run(ctx context.Context, credentials, output string) error {
	cfg, err := loadConfig(credentials)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(output); err == nil {
		return errors.New("output already exists; use a new -output path to reconnect without overwriting it")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check output: %w", err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:8091")
	if err != nil {
		return fmt.Errorf("listen on localhost:8091: %w", err)
	}
	defer ln.Close()
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return err
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	session := &authSession{
		cfg: cfg, state: state, verifier: oauth2.GenerateVerifier(),
		startPath: "/connect/" + state, output: output, done: make(chan error, 1),
	}
	srv := &http.Server{
		Handler: session, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 60 * time.Second,
		IdleTimeout: 30 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.Serve(ln) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	fmt.Printf("Open this local link in your browser, then sign in with the Google account that has Drive access:\nhttp://localhost:8091%s\n", session.startPath)
	fmt.Println("Waiting up to 15 minutes. Credentials and tokens will not be printed.")
	timer := time.NewTimer(15 * time.Minute)
	defer timer.Stop()
	select {
	case err := <-session.done:
		if err != nil {
			return err
		}
		fmt.Println("Google Drive access authorised. Token saved privately to:", output)
		fmt.Println("No Drive files were changed. Next, provide the Shared Drive or review-folder link to verify its permissions.")
		return nil
	case err := <-serverErr:
		return fmt.Errorf("local server: %w", err)
	case <-ctx.Done():
		return errors.New("connection cancelled")
	case <-timer.C:
		return errors.New("sign-in timed out; run the command again")
	}
}

type authSession struct {
	cfg                                *oauth2.Config
	state, verifier, startPath, output string
	done                               chan error
	mu                                 sync.Mutex
	completed                          bool
}

func (s *authSession) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Use GET.", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == s.startPath {
		http.SetCookie(w, &http.Cookie{
			Name: "drive_oauth_state", Value: s.state, Path: "/oauth/google/callback",
			HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 900,
		})
		url := s.cfg.AuthCodeURL(s.state, oauth2.AccessTypeOffline,
			oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(s.verifier))
		http.Redirect(w, r, url, http.StatusFound)
		return
	}
	if r.URL.Path != "/oauth/google/callback" {
		http.NotFound(w, r)
		return
	}
	cookie, err := r.Cookie("drive_oauth_state")
	if err != nil || !sameState(r.URL.Query().Get("state"), s.state) || !sameState(cookie.Value, s.state) {
		http.Error(w, "Sign-in could not be verified. Open the original local connection link and try again.", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.completed {
		s.mu.Unlock()
		http.Error(w, "This connection attempt has already been used.", http.StatusConflict)
		return
	}
	s.completed = true
	s.mu.Unlock()
	fail := func(message string) {
		http.Error(w, message, http.StatusBadRequest)
		s.done <- errors.New(message)
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		fail("Google sign-in was cancelled or rejected. Check the configured test user, then run the command again.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	token, err := s.cfg.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(s.verifier))
	if err != nil {
		// OAuth errors can contain secret response bodies, so never print the raw error.
		fail("Google could not complete the token exchange. Check the client settings and redirect URI, then reconnect.")
		return
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		fail("Google did not return offline access. Reconnect and approve the requested Drive access.")
		return
	}
	if scope, ok := token.Extra("scope").(string); ok && !hasScope(scope, driveScope) {
		fail("Drive access was not granted. Reconnect and approve the requested Drive permission.")
		return
	}
	if err := saveToken(s.output, token); err != nil {
		fail("Could not save the token privately. Check that the output is a new file in a writable directory, then reconnect.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "drive_oauth_state", Path: "/oauth/google/callback", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintln(w, "Google Drive connected. You can close this tab and return to the chat. No Drive files have been changed.")
	s.done <- nil
}

func sameState(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func hasScope(scopes, wanted string) bool {
	for _, scope := range strings.Fields(scopes) {
		if scope == wanted {
			return true
		}
	}
	return false
}

func saveToken(path string, token *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(token); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}
