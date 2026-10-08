package commands

import (
	"sync"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
)

// How long numbered replies stay valid. After that a stray "2" can't act on
// an old list.
const (
	listTTL    = 30 * time.Minute // numbers from "my tasks"
	pendingTTL = 10 * time.Minute // "which one?" after done <name>
	undoTTL    = 10 * time.Minute
)

// session is what the bot remembers about one person's recent messages. It is
// in memory only: a restart forgets it, and people just send "my tasks" again.
type session struct {
	list      []clickup.Task // last "my tasks" reply, in numbered order
	listAt    time.Time
	pending   []clickup.Task // choices offered after an ambiguous done
	pendingAt time.Time
	undo      []clickup.Task // tasks just completed, with their previous status
	undoAt    time.Time
}

type sessions struct {
	mu sync.Mutex
	m  map[string]*session
}

// with runs fn on number's session while holding the lock.
func (s *sessions) with(number string, fn func(*session)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]*session{}
	}
	ss, ok := s.m[number]
	if !ok {
		ss = &session{}
		s.m[number] = ss
	}
	fn(ss)
}

func fresh(at, now time.Time, ttl time.Duration) bool {
	return !at.IsZero() && now.Sub(at) <= ttl
}
