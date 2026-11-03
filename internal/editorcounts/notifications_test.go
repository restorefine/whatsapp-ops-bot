package editorcounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

type message struct {
	to, body, template string
	params             []string
}
type messenger struct {
	messages []message
	err      error
}

func (m *messenger) SendText(_ context.Context, to, body string) error {
	m.messages = append(m.messages, message{to: to, body: body})
	return m.err
}
func (m *messenger) SendTemplate(_ context.Context, to, name, lang string, params []string) error {
	m.messages = append(m.messages, message{to: to, template: name, params: params})
	return m.err
}

func readyBatch(t *testing.T) (*Service, *source, *memoryStore, *time.Time) {
	t.Helper()
	s, cu, store, now := fixture()
	ctx := context.Background()
	if _, err := s.Report(ctx); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	for i := 0; i < 20; i++ {
		cu.tasks = append(cu.tasks, completed(fmt.Sprint(i), 1, "Arpan", *now))
	}
	if _, err := s.Report(ctx); err != nil {
		t.Fatal(err)
	}
	return s, cu, store, now
}

func TestAdminCycleAlertOnceAcrossRestart(t *testing.T) {
	s, cu, store, now := readyBatch(t)
	ctx := context.Background()
	m := &messenger{}
	contacts := whatsapp.NewContacts()
	for _, n := range s.Admins {
		contacts.Record(n, *now)
	}
	d := Delivery{Enabled: true, Messenger: m, Contacts: contacts, Link: func(n string) (string, error) { return "https://example.test/admin/editors/access/private", nil }}
	if err := s.SendQueued(ctx, d); err != nil {
		t.Fatal(err)
	}
	if len(m.messages) != 3 {
		t.Fatalf("sent %d messages", len(m.messages))
	}
	for _, msg := range m.messages {
		if !strings.Contains(msg.body, "20 videos") || !strings.Contains(msg.body, "Ready for payment review") {
			t.Fatal("missing payment-ready evidence")
		}
	}
	s2 := &Service{Source: cu, Store: store, Editors: s.Editors, Admins: s.Admins, Now: s.Now}
	*now = now.Add(2 * time.Minute)
	if _, err := s2.Report(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s2.SendQueued(ctx, d); err != nil {
		t.Fatal(err)
	}
	if len(m.messages) != 3 {
		t.Fatal("restart duplicated alert")
	}
	for _, a := range store.ledger.Alerts {
		if a.State != "sent" {
			t.Fatal("receipt not persisted")
		}
	}
}

func TestClosedWindowWaitsOrUsesDedicatedTemplate(t *testing.T) {
	s, _, store, now := readyBatch(t)
	ctx := context.Background()
	m := &messenger{}
	contacts := whatsapp.NewContacts()
	d := Delivery{Enabled: true, Messenger: m, Contacts: contacts, Language: "en_GB"}
	if err := s.SendQueued(ctx, d); err != nil {
		t.Fatal(err)
	}
	if len(m.messages) != 0 || store.ledger.Alerts[0].State != "queued" {
		t.Fatal("sent outside window without a template")
	}
	contacts.Record(s.Admins[0], *now)
	if err := s.SendQueued(ctx, d); err != nil {
		t.Fatal(err)
	}
	if len(m.messages) != 1 || m.messages[0].to != s.Admins[0] || m.messages[0].template != "" {
		t.Fatal("open window not used")
	}
	d.Template = "rr_editor_cycle_ready"
	if err := s.SendQueued(ctx, d); err != nil {
		t.Fatal(err)
	}
	if len(m.messages) != 3 {
		t.Fatal("closed admin windows did not use template")
	}
	for _, msg := range m.messages[1:] {
		if msg.template != "rr_editor_cycle_ready" || len(msg.params) != 2 || strings.Contains(msg.params[1], "\n") {
			t.Fatal("invalid template variables")
		}
	}
}

func TestUncertainDeliveryAndMissingProofAreHeld(t *testing.T) {
	s, _, store, _ := readyBatch(t)
	ctx := context.Background()
	m := &messenger{err: errors.New("network timeout")}
	d := Delivery{Enabled: true, Messenger: m, Template: "rr_editor_cycle_ready"}
	if err := s.SendQueued(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.SendQueued(ctx, d); err != nil {
		t.Fatal(err)
	}
	if len(m.messages) != 3 {
		t.Fatal("uncertain send retried")
	}
	for _, a := range store.ledger.Alerts {
		if a.State != "uncertain" {
			t.Fatal("uncertain receipt not held")
		}
	}
	s, _, store, _ = readyBatch(t)
	store.ledger.Editors["arpan"][0].Issue = "Reopened"
	m = &messenger{}
	d.Messenger = m
	if err := s.SendQueued(ctx, d); err != nil {
		t.Fatal(err)
	}
	if len(m.messages) != 0 {
		t.Fatal("batch with incomplete evidence advertised ready for payment")
	}
}

func TestFailedIntentSaveDoesNotSend(t *testing.T) {
	s, _, store, _ := readyBatch(t)
	store.fail = true
	m := &messenger{}
	if err := s.SendQueued(context.Background(), Delivery{Enabled: true, Messenger: m, Template: "rr_editor_cycle_ready"}); err == nil {
		t.Fatal("failed intent save ignored")
	}
	if len(m.messages) != 0 {
		t.Fatal("sent without durable intent")
	}
}
