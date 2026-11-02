package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

func (s *Service) Clients() []Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	clients := []Client{}
	for _, c := range s.state.Clients {
		data, _ := json.Marshal(c)
		var copy Client
		_ = json.Unmarshal(data, &copy)
		clients = append(clients, copy)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].Name < clients[j].Name })
	return clients
}

func (s *Service) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := []Job{}
	for _, job := range s.state.Jobs {
		copy := *job
		jobs = append(jobs, copy)
	}
	return jobs
}

// RetryJob is an explicit operator action; the CLI never sends the job itself.
func (s *Service) RetryJob(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return err
	}
	for _, job := range s.state.Jobs {
		if job.ID == id {
			if job.State != "uncertain" {
				return errors.New("only uncertain deliveries can be manually requeued")
			}
			return s.transaction(ctx, func() error { job.State = "queued"; job.Error = ""; return nil })
		}
	}
	return errors.New("notification not found")
}

// Remove only old delivery receipts from the JSON record, never Drive files.
func (s *Service) pruneJobs() {
	cutoff := s.now().Add(-60 * 24 * time.Hour)
	kept := s.state.Jobs[:0]
	for _, j := range s.state.Jobs {
		if (j.State == "sent" || j.State == "cancelled") && j.CreatedAt.Before(cutoff) {
			continue
		}
		kept = append(kept, j)
	}
	s.state.Jobs = kept
}
