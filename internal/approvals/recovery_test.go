package approvals

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/drive"
)

func TestFailedNewRegistrationDoesNotStayInMemory(t *testing.T) {
	s, d, a, _ := fixture(t)
	d.files["second"] = drive.File{ID: "second", Name: "2026-11-07 - Reception.mp4", MimeType: "video/mp4", Size: "500", Parents: []string{a.Months["2026-11"].Review}}
	d.failSave = true
	if err := s.Poll(context.Background()); err == nil {
		t.Fatal("expected failed save")
	}
	if len(s.Reviews(a.ID)) != 1 {
		t.Fatal("new unsaved map key survived rollback")
	}
	d.failSave = false
	if err := s.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.Reviews(a.ID)) != 2 {
		t.Fatal("retry did not restore registration")
	}
}

func TestWindowLookupDoesNotWaitForDriveLock(t *testing.T) {
	s, _, a, _ := fixture(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	done := make(chan bool, 1)
	go func() { _, ok := s.Client(a.ID); done <- ok && s.KnownNumber(a.Numbers[0]) }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("snapshot is missing client")
		}
	case <-time.After(time.Second):
		t.Fatal("webhook lookup waited on the state lock")
	}
}

func TestInvalidRenameIsHeldAndCorrectionRestoresReview(t *testing.T) {
	s, d, a, _ := fixture(t)
	ctx := context.Background()
	f := d.files["reel"]
	f.Name = "missing date.mp4"
	d.files["reel"] = f
	if err := s.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.Reviews(a.ID)) != 0 {
		t.Fatal("invalid renamed preview stayed visible")
	}
	err := s.Act(ctx, a.ID, Action{ReviewID: "reel", Action: "approved", Version: 1, RequestID: strings.Repeat("x", 20)})
	if err == nil {
		t.Fatal("held review accepted a decision")
	}
	f.Name = "2026-11-06 - Venue tour.mp4"
	d.files["reel"] = f
	if err := s.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	reviews := s.Reviews(a.ID)
	if len(reviews) != 1 || reviews[0].PostingDate != "2026-11-06" {
		t.Fatal("corrected date not restored")
	}
	queued := 0
	for _, j := range s.state.Jobs {
		if j.Kind == "new" && j.State == "queued" {
			queued++
		}
	}
	if queued != 1 {
		t.Fatalf("queued new notifications=%d", queued)
	}
}

func TestWriterLockExcludesAnotherProcessHandle(t *testing.T) {
	path := t.TempDir() + "/portal.lock"
	release, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Lock(path); err == nil {
		second()
		release()
		t.Fatal("second writer was allowed")
	}
	release()
	second, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	second()
}

func TestDecisionKeepsOtherVideosInGroupedReminder(t *testing.T) {
	s, _, a, _ := fixture(t)
	s.mu.Lock()
	s.enqueue(a.ID, []string{"reel", "other-pending-video"}, a.Numbers, "reminder", "old summary")
	s.cancelQueued("reel")
	job := s.state.Jobs[len(s.state.Jobs)-1]
	if job.State != "queued" || len(job.ReviewIDs) != 1 || job.ReviewIDs[0] != "other-pending-video" {
		t.Fatal("another video's reminder was cancelled")
	}
	s.mu.Unlock()
}
