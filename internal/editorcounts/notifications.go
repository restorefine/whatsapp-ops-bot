package editorcounts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

type Delivery struct {
	Enabled            bool
	Messenger          whatsapp.Messenger
	Contacts           *whatsapp.Contacts
	Template, Language string
	Link               func(string) (string, error)
}

func (s *Service) Run(ctx context.Context, d Delivery, log *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		cycleCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if _, err := s.Report(cycleCtx); err != nil {
			log.Error("editor cycle scan failed; audit history retained", "err", err)
		} else if err := s.SendQueued(cycleCtx, d); err != nil {
			log.Error("editor cycle notification needs review", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SendQueued saves intent before sending. Uncertain sends are held rather than
// retried automatically, so a restart cannot send duplicate payment notices.
func (s *Service) SendQueued(ctx context.Context, d Delivery) error {
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	if !d.Enabled || d.Messenger == nil {
		return nil
	}
	admins := map[string]bool{}
	for _, n := range s.Admins {
		admins[n] = true
	}
	names := map[string]string{}
	for _, e := range s.Editors {
		names[e.Key] = e.Name
	}
	for sends := 0; sends < 6; sends++ {
		var selected Alert
		open := false
		err := s.Store.UpdateEditorLedger(ctx, func(l *Ledger) error {
			for i := range l.Alerts {
				a := &l.Alerts[i]
				if a.State == "sending" {
					a.State = "uncertain"
					continue
				}
				if a.State != "queued" {
					continue
				}
				if !admins[a.To] || names[a.EditorKey] == "" {
					a.State = "cancelled"
					continue
				}
				verified := 0
				for _, record := range l.Editors[a.EditorKey] {
					if !record.Imported && record.Batch == a.Batch && record.Issue == "" {
						verified++
					}
				}
				if verified != BatchSize {
					continue
				}
				if d.Contacts != nil {
					if last, ok := d.Contacts.LastMessage(a.To); ok {
						age := s.Now().Sub(last)
						open = age >= 0 && age < whatsapp.Window-10*time.Minute
					}
				}
				if !open && d.Template == "" {
					continue
				}
				a.State = "sending"
				selected = *a
				break
			}
			return nil
		})
		if err != nil {
			return err
		}
		if selected.ID == "" {
			return nil
		}
		link := ""
		if d.Link != nil {
			link, err = d.Link(selected.To)
		}
		if err != nil {
			return s.finishAlert(ctx, selected.ID, "queued")
		}
		name := names[selected.EditorKey]
		text := fmt.Sprintf("%s has completed batch %d: 20 videos. Ready for payment review.\nThe next cycle has started; previous batches are kept for audit.\nThis notification does not mark a payment as made.\n\nEvidence report: %s", name, selected.Batch, link)
		parameter := fmt.Sprintf("Batch %d: 20 videos completed; ready for payment review. Evidence: %s", selected.Batch, link)
		if open {
			err = d.Messenger.SendText(ctx, selected.To, text)
			var apiErr *whatsapp.APIError
			if errors.As(err, &apiErr) && apiErr.OutsideWindow() {
				if d.Template == "" {
					if saveErr := s.finishAlert(ctx, selected.ID, "queued"); saveErr != nil {
						return saveErr
					}
					return nil
				}
				err = d.Messenger.SendTemplate(ctx, selected.To, d.Template, d.Language, []string{name, parameter})
			}
		} else {
			err = d.Messenger.SendTemplate(ctx, selected.To, d.Template, d.Language, []string{name, strings.Join(strings.Fields(parameter), " ")})
		}
		state := "sent"
		if err != nil {
			state = "uncertain"
		}
		if err := s.finishAlert(ctx, selected.ID, state); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) finishAlert(ctx context.Context, id, state string) error {
	return s.Store.UpdateEditorLedger(ctx, func(l *Ledger) error {
		for i := range l.Alerts {
			if l.Alerts[i].ID == id {
				l.Alerts[i].State = state
				return nil
			}
		}
		return errors.New("editor notification receipt not found")
	})
}
