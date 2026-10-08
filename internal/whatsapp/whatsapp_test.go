package whatsapp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

const (
	secret = "test-app-secret"
	owner  = "9779812345678"
	owner2 = "447700900123"
)

func TestValidSignature(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	good := Sign(secret, body)
	tests := []struct {
		name   string
		secret string
		body   []byte
		header string
		want   bool
	}{
		{"valid", secret, body, good, true},
		{"wrong secret", "other", body, good, false},
		{"tampered body", secret, []byte(`{"object":"whatsapp_business_accounT"}`), good, false},
		{"missing header", secret, body, "", false},
		{"missing prefix", secret, body, strings.TrimPrefix(good, "sha256="), false},
		{"not hex", secret, body, "sha256=zzzz", false},
		{"empty secret", "", body, Sign("", body), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidSignature(tt.secret, tt.body, tt.header); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSplit(t *testing.T) {
	t.Run("short text is one part", func(t *testing.T) {
		if got := Split("hello", 10); len(got) != 1 || got[0] != "hello" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("prefers line breaks", func(t *testing.T) {
		got := Split("aaaa bbbb\ncccc dddd\neeee", 12)
		want := []string{"aaaa bbbb", "cccc dddd", "eeee"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("does not cut inside a monospace block that fits", func(t *testing.T) {
		text := "intro line one\n```\nrow 1\nrow 2\n```\nafter the table"
		got := Split(text, 30)
		want := []string{"intro line one", "```\nrow 1\nrow 2\n```", "after the table"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("closes and reopens a block that is too long", func(t *testing.T) {
		text := "```\nrow one\nrow two\nrow three\nrow four\n```"
		parts := Split(text, 25)
		if len(parts) < 2 {
			t.Fatalf("expected a split, got %q", parts)
		}
		for _, p := range parts {
			if strings.Count(p, "```")%2 != 0 {
				t.Fatalf("part has an unclosed block: %q", p)
			}
		}
	})
	t.Run("hard cuts long words", func(t *testing.T) {
		got := Split(strings.Repeat("x", 25), 10)
		if len(got) != 3 || got[0] != strings.Repeat("x", 10) || got[2] != "xxxxx" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("counts runes and keeps content", func(t *testing.T) {
		var lines []string
		for i := 0; i < 400; i++ {
			lines = append(lines, "✅ tâsk number with ümlauts")
		}
		text := strings.Join(lines, "\n")
		parts := Split(text, MaxMessageLen)
		if len(parts) < 2 {
			t.Fatalf("expected several parts, got %d", len(parts))
		}
		for _, p := range parts {
			if n := len([]rune(p)); n > MaxMessageLen {
				t.Fatalf("part has %d runes", n)
			}
		}
		if strings.Join(parts, "\n") != text {
			t.Fatal("content lost while splitting")
		}
	})
}

type recorder struct {
	mu   sync.Mutex
	msgs []InboundMessage
	done chan struct{}
}

func (r *recorder) Process(_ context.Context, m InboundMessage) {
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	r.mu.Unlock()
	r.done <- struct{}{}
}

func newTestWebhook(t *testing.T) (*Webhook, *recorder) {
	rec := &recorder{done: make(chan struct{}, 10)}
	w := NewWebhook(WebhookConfig{VerifyToken: "vt", AppSecret: secret, Numbers: []string{owner, owner2}, PhoneNumberID: "111"}, rec, quiet)
	w.Start(context.Background())
	t.Cleanup(w.Stop)
	return w, rec
}

func post(w *Webhook, body []byte, sig string) int {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", sig)
	rr := httptest.NewRecorder()
	w.Receive(rr, req)
	return rr.Code
}

func fixture(t *testing.T, from, id, text string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/webhook_text_message.json")
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	msg := p["entry"].([]any)[0].(map[string]any)["changes"].([]any)[0].(map[string]any)["value"].(map[string]any)["messages"].([]any)[0].(map[string]any)
	msg["from"], msg["id"] = from, id
	msg["text"].(map[string]any)["body"] = text
	out, _ := json.Marshal(p)
	return out
}

func waitFor(t *testing.T, rec *recorder) {
	t.Helper()
	select {
	case <-rec.done:
	case <-time.After(2 * time.Second):
		t.Fatal("message was not processed")
	}
}

func TestWebhookVerify(t *testing.T) {
	w, _ := newTestWebhook(t)
	tests := []struct {
		query string
		code  int
		body  string
	}{
		{"hub.mode=subscribe&hub.verify_token=vt&hub.challenge=12345", 200, "12345"},
		{"hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=12345", 403, ""},
		{"hub.challenge=12345", 403, ""},
	}
	for _, tt := range tests {
		rr := httptest.NewRecorder()
		w.Verify(rr, httptest.NewRequest(http.MethodGet, "/webhook?"+tt.query, nil))
		if rr.Code != tt.code || (tt.body != "" && rr.Body.String() != tt.body) {
			t.Errorf("%s: got %d %q", tt.query, rr.Code, rr.Body.String())
		}
	}
}

func TestWebhookReceive(t *testing.T) {
	w, rec := newTestWebhook(t)

	body := fixture(t, owner, "wamid.1", "/help")
	if code := post(w, body, "sha256=00"); code != http.StatusUnauthorized {
		t.Fatalf("bad signature: got %d, want 401", code)
	}

	if code := post(w, body, Sign(secret, body)); code != http.StatusOK {
		t.Fatalf("got %d", code)
	}
	waitFor(t, rec)

	// Redelivery of the same wamid is ignored.
	if code := post(w, body, Sign(secret, body)); code != http.StatusOK {
		t.Fatalf("got %d", code)
	}

	// A second owner is also accepted.
	second := fixture(t, owner2, "wamid.3", "/team")
	if code := post(w, second, Sign(secret, second)); code != http.StatusOK {
		t.Fatalf("got %d", code)
	}
	waitFor(t, rec)

	// Messages from anyone else are ignored.
	stranger := fixture(t, "15550001111", "wamid.2", "/help")
	if code := post(w, stranger, Sign(secret, stranger)); code != http.StatusOK {
		t.Fatalf("got %d", code)
	}

	// Status-only events are acknowledged and ignored.
	status := []byte(`{"object":"whatsapp_business_account","entry":[{"changes":[{"field":"messages","value":{"statuses":[{"id":"wamid.1","status":"delivered"}]}}]}]}`)
	if code := post(w, status, Sign(secret, status)); code != http.StatusOK {
		t.Fatalf("got %d", code)
	}

	time.Sleep(100 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.msgs) != 2 {
		t.Fatalf("processed %d messages, want 2: %+v", len(rec.msgs), rec.msgs)
	}
	if m := rec.msgs[0]; m.From != owner || m.Text != "/help" || m.Type != "text" || m.ID != "wamid.1" {
		t.Fatalf("unexpected message %+v", m)
	}
	if m := rec.msgs[1]; m.From != owner2 || m.Text != "/team" {
		t.Fatalf("unexpected message %+v", m)
	}
}

func TestCloudClientSendText(t *testing.T) {
	var calls atomic.Int32
	var bodies []map[string]any
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path != "/v25.0/PNID/messages" || r.Header.Get("Authorization") != "Bearer TOKEN" {
			t.Errorf("unexpected request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		if n == 1 { // first attempt hits a transient error
			rw.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		_, _ = io.WriteString(rw, `{"messages":[{"id":"wamid.x"}]}`)
	}))
	defer srv.Close()

	c := NewCloudClient(srv.URL, "v25.0", "PNID", "TOKEN", quiet)
	c.BaseBackoff = time.Millisecond
	long := strings.Repeat("line of text\n", 400) // ~5200 chars, two messages
	if err := c.SendText(context.Background(), owner, long); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("sent %d messages, want 2", len(bodies))
	}
	if bodies[0]["to"] != owner || bodies[0]["type"] != "text" {
		t.Fatalf("unexpected payload %v", bodies[0])
	}
}

func TestCloudClientOutsideWindowIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		rw.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(rw, `{"error":{"message":"Re-engagement message","code":131047}}`)
	}))
	defer srv.Close()

	c := NewCloudClient(srv.URL, "v25.0", "PNID", "TOKEN", quiet)
	err := c.SendText(context.Background(), owner, "hi")
	apiErr, ok := err.(*APIError)
	if !ok || !apiErr.OutsideWindow() {
		t.Fatalf("expected outside-window error, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("made %d calls, want 1", calls.Load())
	}
}

func TestWebhookRecordsKnownNumbers(t *testing.T) {
	const member = "447700900456"
	rec := &recorder{done: make(chan struct{}, 10)}
	contacts := NewContacts()
	w := NewWebhook(WebhookConfig{AppSecret: secret, Numbers: []string{owner, member}, Contacts: contacts}, rec, quiet)
	w.Start(context.Background())

	for i, from := range []string{member, "15550001111", owner} {
		body := fixture(t, from, "wamid.team"+string(rune('0'+i)), "ok")
		if code := post(w, body, Sign(secret, body)); code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
	}
	w.Stop()

	if len(rec.msgs) != 2 || rec.msgs[0].From != member || rec.msgs[1].From != owner {
		t.Fatalf("known numbers should be processed and strangers dropped, got %+v", rec.msgs)
	}
	// The fixture's timestamp is 1790000000.
	if last, ok := contacts.LastMessage(member); !ok || !last.Equal(time.Unix(1790000000, 0)) {
		t.Errorf("team member last message = %v, %v", last, ok)
	}
	if _, ok := contacts.LastMessage("15550001111"); ok {
		t.Error("unknown numbers must not be recorded")
	}
}

func TestSentAt(t *testing.T) {
	now := time.Unix(2000, 0)
	for ts, want := range map[string]time.Time{
		"1500": time.Unix(1500, 0),
		"":     now,
		"abc":  now,
		"3000": now, // in the future
	} {
		if got := sentAt(ts, now); !got.Equal(want) {
			t.Errorf("sentAt(%q) = %v, want %v", ts, got, want)
		}
	}
}
