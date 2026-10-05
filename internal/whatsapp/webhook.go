package whatsapp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"
)

// InboundMessage is a message sent to the bot.
type InboundMessage struct {
	ID   string // wamid
	From string // sender number, digits only
	Type string // text, image, audio, ...
	Text string // body for text messages
}

// Processor handles one inbound message. It runs on a background worker.
type Processor interface {
	Process(ctx context.Context, msg InboundMessage)
}

// WebhookConfig configures the webhook handler.
type WebhookConfig struct {
	VerifyToken   string
	AppSecret     string
	OwnerNumbers  []string // only these senders get a reply
	PhoneNumberID string   // optional; when set, events for other numbers are ignored
	Workers       int
	QueueSize     int
	DedupeTTL     time.Duration
}

// Webhook serves GET and POST /webhook and processes messages asynchronously.
type Webhook struct {
	cfg   WebhookConfig
	proc  Processor
	log   *slog.Logger
	queue chan InboundMessage
	seen  *seenSet
	wg    sync.WaitGroup
}

// NewWebhook creates the handler. Call Start before serving and Stop on shutdown.
func NewWebhook(cfg WebhookConfig, proc Processor, log *slog.Logger) *Webhook {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 64
	}
	if cfg.DedupeTTL <= 0 {
		cfg.DedupeTTL = 24 * time.Hour
	}
	return &Webhook{
		cfg:   cfg,
		proc:  proc,
		log:   log,
		queue: make(chan InboundMessage, cfg.QueueSize),
		seen:  newSeenSet(cfg.DedupeTTL),
	}
}

// Start launches the worker goroutines. ctx is passed to the processor.
func (w *Webhook) Start(ctx context.Context) {
	for i := 0; i < w.cfg.Workers; i++ {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			for msg := range w.queue {
				w.process(ctx, msg)
			}
		}()
	}
}

// Stop stops accepting work and waits for queued messages to finish.
// The HTTP server must be shut down first so nothing writes to the queue.
func (w *Webhook) Stop() {
	close(w.queue)
	w.wg.Wait()
}

func (w *Webhook) process(ctx context.Context, msg InboundMessage) {
	defer func() {
		if r := recover(); r != nil {
			w.log.Error("panic while processing message", "panic", r, "wamid", msg.ID)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	w.proc.Process(ctx, msg)
}

// Verify handles GET /webhook, Meta's subscription handshake.
func (w *Webhook) Verify(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("hub.mode") == "subscribe" && w.cfg.VerifyToken != "" && q.Get("hub.verify_token") == w.cfg.VerifyToken {
		rw.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(rw, q.Get("hub.challenge"))
		w.log.Info("webhook verified by meta")
		return
	}
	w.log.Warn("webhook verification failed")
	http.Error(rw, "forbidden", http.StatusForbidden)
}

// Receive handles POST /webhook.
func (w *Webhook) Receive(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, 1<<20))
	if err != nil {
		http.Error(rw, "bad request", http.StatusBadRequest)
		return
	}
	if !ValidSignature(w.cfg.AppSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		w.log.Warn("webhook: rejected request with invalid signature", "remote", r.RemoteAddr)
		http.Error(rw, "invalid signature", http.StatusUnauthorized)
		return
	}

	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		w.log.Warn("webhook: invalid json", "err", err)
		// Signed by Meta but unparseable: acknowledge so it is not retried forever.
		rw.WriteHeader(http.StatusOK)
		return
	}

	msgs := w.extract(p)
	for i, msg := range msgs {
		select {
		case w.queue <- msg:
		default:
			// Let Meta retry later; dedupe skips the ones already queued.
			for _, m := range msgs[i:] {
				w.seen.forget(m.ID)
			}
			w.log.Error("webhook: queue full, asking meta to retry", "wamid", msg.ID)
			http.Error(rw, "busy", http.StatusServiceUnavailable)
			return
		}
	}
	rw.WriteHeader(http.StatusOK)
}

// extract returns the owner's new messages from a webhook payload.
func (w *Webhook) extract(p payload) []InboundMessage {
	var out []InboundMessage
	for _, entry := range p.Entry {
		for _, change := range entry.Changes {
			v := change.Value
			if change.Field != "messages" {
				w.log.Debug("webhook: ignoring field", "field", change.Field)
				continue
			}
			if w.cfg.PhoneNumberID != "" && v.Metadata.PhoneNumberID != "" && v.Metadata.PhoneNumberID != w.cfg.PhoneNumberID {
				w.log.Debug("webhook: ignoring event for another phone number")
				continue
			}
			if len(v.Statuses) > 0 {
				w.log.Debug("webhook: ignoring status events", "count", len(v.Statuses))
			}
			for _, m := range v.Messages {
				if !slices.Contains(w.cfg.OwnerNumbers, m.From) {
					w.log.Debug("webhook: ignoring message from non-owner")
					continue
				}
				if m.ID == "" || !w.seen.add(m.ID) {
					w.log.Debug("webhook: duplicate message ignored", "wamid", m.ID)
					continue
				}
				out = append(out, InboundMessage{ID: m.ID, From: m.From, Type: m.Type, Text: m.Text.Body})
			}
		}
	}
	return out
}

type payload struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Changes []struct {
			Field string `json:"field"`
			Value struct {
				Metadata struct {
					PhoneNumberID string `json:"phone_number_id"`
				} `json:"metadata"`
				Messages []struct {
					From string `json:"from"`
					ID   string `json:"id"`
					Type string `json:"type"`
					Text struct {
						Body string `json:"body"`
					} `json:"text"`
				} `json:"messages"`
				Statuses []json.RawMessage `json:"statuses"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// seenSet remembers message IDs for a while so Meta's redeliveries are ignored.
// It is in memory only: every command is read-only, so a duplicate reply after
// a restart is harmless.
type seenSet struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[string]time.Time
	now   func() time.Time
}

func newSeenSet(ttl time.Duration) *seenSet {
	return &seenSet{ttl: ttl, items: map[string]time.Time{}, now: time.Now}
}

// add records id and reports whether it was new.
func (s *seenSet) add(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if len(s.items) > 1000 {
		for k, t := range s.items {
			if now.Sub(t) > s.ttl {
				delete(s.items, k)
			}
		}
	}
	if t, ok := s.items[id]; ok && now.Sub(t) <= s.ttl {
		return false
	}
	s.items[id] = now
	return true
}

func (s *seenSet) forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
}
