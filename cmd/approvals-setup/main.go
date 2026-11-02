// Command approvals-setup onboards clients and checks the private Drive portal.
// Run it while the bot's portal writer is stopped. It never sends WhatsApp.
package main

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/approvals"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/drive"
)

func setting(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func main() {
	credentials := flag.String("credentials", setting("APPROVALS_CREDENTIALS_FILE", ".secrets/drive-client.json"), "OAuth client JSON path")
	token := flag.String("token", setting("APPROVALS_TOKEN_FILE", ".secrets/drive-token.json"), "private OAuth token path")
	keyFile := flag.String("key-file", setting("APPROVALS_KEY_FILE", ".secrets/portal-signing-key"), "persistent signing-key file; created if missing")
	origin := flag.String("origin", setting("APPROVALS_ORIGIN", "https://bot.restorefine.co.uk"), "public HTTPS origin, or local HTTP origin for local tests")
	staticDir := flag.String("static-dir", setting("APPROVALS_STATIC_DIR", "portal/out"), "Next.js export directory")
	client := flag.String("client", "", "create/update a client by its full name")
	numbers := flag.String("numbers", "", "approval contact numbers, separated by semicolons or commas; country codes required")
	roster := flag.String("clients-file", "", "bulk CSV with header client,whatsapp")
	month := flag.String("month", "", "YYYY-MM; defaults to current UK month")
	rotate := flag.Bool("rotate", false, "replace the selected client's link and revoke its existing sessions")
	jobs := flag.Bool("jobs", false, "show notification state (does not send)")
	retry := flag.String("retry-job", "", "requeue an uncertain delivery after checking whether it reached the recipient")
	serve := flag.String("serve", "", "serve the real portal locally, e.g. 127.0.0.1:8092; WhatsApp remains disabled")
	check := flag.Bool("check", false, "read-only Drive connectivity/root check, without creating portal folders")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *credentials, *token, *keyFile, *origin, *staticDir, *client, *numbers, *roster, *month, *rotate, *jobs, *retry, *serve, *check); err != nil {
		fmt.Fprintln(os.Stderr, "Portal setup:", err)
		os.Exit(1)
	}
}

type entry struct {
	name    string
	numbers []string
}

var phone = regexp.MustCompile(`^[1-9][0-9]{7,14}$`)

func contacts(raw string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' }) {
		n := strings.TrimPrefix(strings.TrimSpace(value), "+")
		if !phone.MatchString(n) {
			return nil, errors.New("use WhatsApp numbers with country code and digits only")
		}
		if !seen[n] {
			out = append(out, n)
			seen[n] = true
		}
	}
	return out, nil
}

func readRoster(path string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil || len(header) != 2 || strings.ToLower(strings.TrimSpace(header[0])) != "client" || strings.ToLower(strings.TrimSpace(header[1])) != "whatsapp" {
		return nil, errors.New("CSV must start with client,whatsapp")
	}
	out := []entry{}
	seen := map[string]bool{}
	for row := 2; ; row++ {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: %w", row, err)
		}
		name := strings.TrimSpace(record[0])
		if name == "" || len(name) > 100 || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
			return nil, fmt.Errorf("invalid client name on CSV row %d", row)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, fmt.Errorf("duplicate client on CSV row %d", row)
		}
		seen[key] = true
		numbers, err := contacts(record[1])
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: %w", row, err)
		}
		out = append(out, entry{name, numbers})
	}
	if len(out) == 0 {
		return nil, errors.New("CSV has no clients")
	}
	return out, nil
}

func signingKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) < 32 {
			return nil, errors.New("existing signing key is too short; do not overwrite it")
		}
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return key, nil
}

func run(ctx context.Context, credentials, token, keyFile, origin, staticDir, client, numbers, roster, month string, rotate, jobs bool, retry, serve string, check bool) error {
	if client != "" && roster != "" {
		return errors.New("use either -client or -clients-file")
	}
	if rotate && (client == "" || roster != "") {
		return errors.New("-rotate requires one -client")
	}
	entries := []entry{}
	if roster != "" {
		var err error
		entries, err = readRoster(roster)
		if err != nil {
			return err
		}
	}
	if client != "" && !rotate {
		var ns []string
		if numbers != "" {
			var err error
			ns, err = contacts(numbers)
			if err != nil {
				return err
			}
		}
		entries = append(entries, entry{client, ns})
	}
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		return err
	}
	if month == "" {
		month = time.Now().In(loc).Format("2006-01")
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		return errors.New("month must use YYYY-MM")
	}
	unlock, err := approvals.Lock(filepath.Join(filepath.Dir(keyFile), "portal.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	d, err := drive.Open(ctx, credentials, token)
	if err != nil {
		return err
	}
	if check {
		fmt.Println("Google access works. Authorised review root is private and accessible. No Drive content changed.")
		return nil
	}
	key, err := signingKey(keyFile)
	if err != nil {
		return err
	}
	// Validate origin/export before any Drive folder creation.
	if _, err := approvals.NewWeb(nil, key, origin, staticDir); err != nil {
		return err
	}
	s, err := approvals.New(ctx, d, loc)
	if err != nil {
		return err
	}
	web, err := approvals.NewWeb(s, key, origin, staticDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := s.AddClient(ctx, e.name, e.numbers, month); err != nil {
			return fmt.Errorf("onboard %s: %w", e.name, err)
		}
	}
	if rotate {
		if _, err := s.Rotate(ctx, client); err != nil {
			return err
		}
	}
	if retry != "" {
		if err := s.RetryJob(ctx, retry); err != nil {
			return err
		}
	}
	if err := s.Poll(ctx); err != nil {
		return err
	}
	for _, c := range s.Clients() {
		if client != "" && !strings.EqualFold(c.Name, client) {
			continue
		}
		fmt.Println(c.Name)
		fmt.Println("Portal:", web.Link(c))
		if folders, ok := c.Months[month]; ok {
			fmt.Println("Upload:", "https://drive.google.com/drive/folders/"+folders.Review)
		}
		fmt.Printf("Approval contacts: %d\n\n", len(c.Numbers))
	}
	if jobs {
		for _, j := range s.Jobs() {
			suffix := j.To
			if len(suffix) > 4 {
				suffix = suffix[len(suffix)-4:]
			}
			fmt.Printf("%s  %s  %s  recipient ending %s\n", j.ID, j.Kind, j.State, suffix)
		}
	}
	fmt.Println("Setup complete. No WhatsApp notifications were sent.")
	if serve == "" {
		return nil
	}
	if serve != "127.0.0.1:8092" {
		return errors.New("local verification server must use 127.0.0.1:8092")
	}
	if origin != "http://localhost:8092" && origin != "http://127.0.0.1:8092" {
		return errors.New("local server requires -origin http://localhost:8092")
	}
	server := &http.Server{Addr: serve, Handler: web, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second}
	go s.Run(ctx, approvals.Delivery{}, slog.Default())
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Println("Local Drive-backed portal listening at", origin)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
