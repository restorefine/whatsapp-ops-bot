package approvals

import (
	"context"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/editorcounts"
)

// UpdateEditorLedger uses the same lock and recovery path as client reviews.
// ClickUp fetching happens before this callback; it contains no network reads.
func (s *Service) UpdateEditorLedger(ctx context.Context, update func(*editorcounts.Ledger) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return err
	}
	return s.transaction(ctx, func() error {
		if err := update(&s.state.EditorLedger); err != nil {
			return err
		}
		return editorcounts.Validate(s.state.EditorLedger)
	})
}
