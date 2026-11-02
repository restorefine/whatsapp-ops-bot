package approvals

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
	Enabled          bool
	Messenger        whatsapp.Messenger
	Contacts         *whatsapp.Contacts
	NewTemplate      string
	ReminderTemplate string
	InternalTemplate string
	Language         string
	Link             func(Client) string
	Deadlines        map[string][2]int
}

func (s *Service) Run(ctx context.Context, delivery Delivery, log *slog.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var lastPoll time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		if time.Since(lastPoll) >= 2*time.Minute {
			pollCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			if err := s.Poll(pollCtx); err != nil {
				log.Error("approvals: Drive scan failed", "err", err)
			}
			cancel()
			lastPoll = time.Now()
		}
		s.mu.Lock()
		err := s.reconcile(ctx)
		s.mu.Unlock()
		if err != nil {
			log.Error("approvals: decision move pending", "err", err)
		}
		if delivery.Enabled {
			if err := s.schedule(ctx, delivery); err != nil {
				log.Error("approvals: reminder schedule failed", "err", err)
			}
			if err := s.SendQueued(ctx, delivery); err != nil {
				log.Error("approvals: notification progress could not be saved", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) deadline(client *Client, date string, delivery Delivery) time.Time {
	d, _ := time.ParseInLocation("2006-01-02", date, s.loc)
	at := [2]int{17, 0}
	if configured, ok := delivery.Deadlines["*"]; ok {
		at = configured
	}
	name := strings.ToLower(strings.ReplaceAll(client.Name, " ", ""))
	for key, value := range delivery.Deadlines {
		if strings.ToLower(strings.ReplaceAll(key, " ", "")) == name {
			at = value
		}
	}
	return time.Date(d.Year(), d.Month(), d.Day(), at[0], at[1], 0, 0, s.loc)
}

func (s *Service) schedule(ctx context.Context, delivery Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return err
	}
	return s.transaction(ctx, func() error {
		now := s.now()
		for _, client := range s.state.Clients {
			var ids []string
			var titles []string
			for _, review := range s.state.Reviews {
				if review.ClientID != client.ID || review.Status != "pending" || s.state.Invalid[review.ID] != "" || review.NotifiedAt == nil {
					continue
				}
				last := *review.NotifiedAt
				if review.ReminderAt != nil && review.ReminderAt.After(last) {
					last = *review.ReminderAt
				}
				urgent := !review.UrgentQueued && !now.Before(s.deadline(client, review.PostingDate, delivery).Add(-24*time.Hour)) && now.Sub(last) >= 6*time.Hour
				if now.Sub(last) < 24*time.Hour && !urgent {
					continue
				}
				if urgent {
					review.UrgentQueued = true
				}
				review.ReminderAt = &now
				ids = append(ids, review.ID)
				titles = append(titles, review.Title+" ("+review.PostingDate+")")
			}
			if len(ids) == 0 {
				continue
			}
			text := fmt.Sprintf("%s: %d video(s) are still waiting for approval.\n%s", client.Name, len(ids), strings.Join(titles, "\n"))
			s.enqueue(client.ID, ids, client.Numbers, "reminder", text)
			s.enqueue(client.ID, ids, s.Staff, "internal-reminder", text)
		}
		return nil
	})
}

// Remind is wired only into the admin /remind videos namespace. The task
// reminder path does not call this sender or any of these paid templates.
func (s *Service) Remind(ctx context.Context, name string, delivery Delivery) string {
	if strings.TrimSpace(name) == "" {
		return "Use /remind videos <client>, for example /remind videos Blanc Events."
	}
	if !delivery.Enabled {
		return "Video reminders are disabled for setup. Enable APPROVALS_NOTIFY_ENABLED after the client numbers and templates are configured."
	}
	s.mu.Lock()
	if err := s.prepare(ctx); err != nil {
		s.mu.Unlock()
		return err.Error()
	}
	var found []*Client
	for _, client := range s.state.Clients {
		if strings.EqualFold(client.Name, strings.TrimSpace(name)) {
			found = append(found, client)
		}
	}
	if len(found) != 1 {
		s.mu.Unlock()
		return "No exact client match. Use the full client folder name after /remind videos."
	}
	client := found[0]
	ids := []string{}
	titles := []string{}
	overdue := false
	for _, review := range s.state.Reviews {
		if review.ClientID == client.ID && review.Status == "pending" && s.state.Invalid[review.ID] == "" {
			ids = append(ids, review.ID)
			titles = append(titles, review.Title+" ("+review.PostingDate+")")
			overdue = overdue || s.now().After(s.deadline(client, review.PostingDate, delivery))
		}
	}
	if len(ids) == 0 {
		s.mu.Unlock()
		return client.Name + " has no videos waiting for approval. No reminder was sent."
	}
	if len(client.Numbers) == 0 {
		s.mu.Unlock()
		return "No approval contact is configured for " + client.Name + "."
	}
	text := fmt.Sprintf("%s: please review %d pending video(s).\n%s", client.Name, len(ids), strings.Join(titles, "\n"))
	err := s.transaction(ctx, func() error {
		s.enqueue(client.ID, ids, client.Numbers, "reminder", text)
		if overdue {
			s.enqueue(client.ID, ids, s.Staff, "internal-reminder", text)
		}
		now := s.now()
		for _, id := range ids {
			s.state.Reviews[id].ReminderAt = &now
		}
		return nil
	})
	s.mu.Unlock()
	if err != nil {
		return "Couldn't queue the video reminder. Please try again."
	}
	if err := s.SendQueued(ctx, delivery); err != nil {
		return "The reminder is recorded, but delivery could not be confirmed. Check before repeating it."
	}
	return fmt.Sprintf("Processed the reminder for %s (%d pending videos). Delivery is recorded in the portal's private Drive state; uncertain sends are held for checking.", client.Name, len(ids))
}

func (s *Service) SendQueued(ctx context.Context, delivery Delivery) error {
	if !delivery.Enabled || delivery.Messenger == nil {
		return nil
	}
	s.mu.Lock()
	ids := []string{}
	for _, job := range s.state.Jobs {
		if job.State == "queued" {
			ids = append(ids, job.ID)
		}
	}
	s.mu.Unlock()
	sent := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if sent >= 10 {
			break
		}
		s.mu.Lock()
		if err := s.prepare(ctx); err != nil {
			s.mu.Unlock()
			return err
		}
		var job *Job
		for _, candidate := range s.state.Jobs {
			if candidate.ID == id {
				job = candidate
				break
			}
		}
		if job == nil || job.State != "queued" {
			s.mu.Unlock()
			continue
		}
		client := s.state.Clients[job.ClientID]
		if client == nil {
			s.mu.Unlock()
			continue
		}
		internal := strings.HasPrefix(job.Kind, "internal")
		if !internal {
			known := false
			for _, n := range client.Numbers {
				known = known || n == job.To
			}
			if !known {
				job.State = "cancelled"
				err := s.save(ctx)
				s.mu.Unlock()
				if err != nil {
					return err
				}
				continue
			}
		}
		pendingOnly := job.Kind == "new" || strings.Contains(job.Kind, "reminder")
		text := job.Text
		if pendingOnly {
			titles := []string{}
			refs := []string{}
			for _, ref := range job.ReviewIDs {
				if r := s.state.Reviews[ref]; r != nil && r.Status == "pending" && s.state.Invalid[ref] == "" {
					refs = append(refs, ref)
					titles = append(titles, r.Title+" ("+r.PostingDate+")")
				}
			}
			if len(refs) == 0 {
				job.State = "cancelled"
				err := s.save(ctx)
				s.mu.Unlock()
				if err != nil {
					return err
				}
				continue
			}
			job.ReviewIDs = refs
			text = fmt.Sprintf("%s: %d video(s) waiting for approval.\n%s", client.Name, len(refs), strings.Join(titles, "\n"))
		}
		if len([]rune(text)) > 2600 {
			text = string([]rune(text)[:2600]) + "…"
		}
		if !internal && delivery.Link != nil {
			text += "\n\n" + delivery.Link(*client)
		}
		template := delivery.InternalTemplate
		if job.Kind == "new" {
			template = delivery.NewTemplate
		}
		if job.Kind == "reminder" {
			template = delivery.ReminderTemplate
		}
		open := false
		if delivery.Contacts != nil {
			if last, ok := delivery.Contacts.LastMessage(job.To); ok {
				age := s.now().Sub(last)
				open = age >= 0 && age < whatsapp.Window-10*time.Minute
			}
		}
		if !open && template == "" {
			s.mu.Unlock()
			continue
		}
		job.State = "sending"
		if err := s.save(ctx); err != nil {
			s.mu.Unlock()
			return err
		}
		to, name, kind := job.To, client.Name, job.Kind
		parameter := strings.Join(strings.Fields(text), " ")
		if len([]rune(parameter)) > 900 {
			parameter = string([]rune(parameter)[:900]) + "…"
		}
		if !internal && delivery.Link != nil {
			parameter = delivery.Link(*client)
		}
		s.mu.Unlock()

		// Never hold the state mutex across Meta calls. Incoming windows and client
		// reviews stay available while a notification is in flight.
		var err error
		if open {
			err = delivery.Messenger.SendText(ctx, to, text)
			var apiErr *whatsapp.APIError
			if errors.As(err, &apiErr) && apiErr.OutsideWindow() && template != "" {
				err = delivery.Messenger.SendTemplate(ctx, to, template, delivery.Language, []string{name, parameter})
			}
		} else {
			err = delivery.Messenger.SendTemplate(ctx, to, template, delivery.Language, []string{name, parameter})
		}
		sent++
		s.mu.Lock()
		if reloadErr := s.prepare(ctx); reloadErr != nil {
			s.mu.Unlock()
			return reloadErr
		}
		for _, current := range s.state.Jobs {
			if current.ID != id {
				continue
			}
			if err != nil {
				current.State = "uncertain"
				current.Error = "Delivery failed or could not be confirmed. Check before retrying."
			} else {
				current.State = "sent"
				current.Error = ""
				if kind == "new" {
					now := s.now()
					for _, ref := range current.ReviewIDs {
						if r := s.state.Reviews[ref]; r != nil && r.NotifiedAt == nil {
							r.NotifiedAt = &now
						}
					}
				}
			}
			break
		}
		saveErr := s.save(ctx)
		s.mu.Unlock()
		if saveErr != nil {
			return saveErr
		}
	}
	return nil
}
