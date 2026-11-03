package approvals

import (
	"context"
	"testing"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/editorcounts"
)

func TestEditorLedgerSharesDurablePortalTransaction(t *testing.T) {
	s, d, client, _ := fixture(t)
	ctx := context.Background()
	update := func(l *editorcounts.Ledger) error {
		l.StartedAt = time.Now().UTC()
		l.Joined = map[string]time.Time{"arpan": l.StartedAt}
		l.Editors = map[string][]editorcounts.Record{"arpan": {{TaskID: "proof", Title: "Video", CompletedAt: l.StartedAt, RecordedAt: l.StartedAt, Batch: 1, Position: 1}}}
		return nil
	}
	d.failSave = true
	if err := s.UpdateEditorLedger(ctx, update); err == nil {
		t.Fatal("failed Drive save accepted")
	}
	if len(s.state.EditorLedger.Editors) != 0 {
		t.Fatal("unsaved ledger survived rollback")
	}
	d.failSave = false
	if err := s.UpdateEditorLedger(ctx, update); err != nil {
		t.Fatal(err)
	}
	if len(s.Reviews(client.ID)) != 1 {
		t.Fatal("editor save changed client review state")
	}
	var restored State
	if err := d.ReadJSON(ctx, s.stateID, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.EditorLedger.Editors["arpan"]) != 1 || len(restored.Reviews) != len(s.state.Reviews) {
		t.Fatal("durable state lost editor or client records")
	}
}
