package whatsapp

import (
	"sync"
	"time"
)

// Window is how long after someone's last message the bot may send them
// free-form text for free (Meta's customer service window).
const Window = 24 * time.Hour

// Contacts remembers when each number last messaged the business number, so
// the bot knows whose 24 hour window is open. It is in memory only: after a
// restart every window looks closed until that person messages again, which
// only means /remind offers a tap-to-send link instead of sending directly.
type Contacts struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func NewContacts() *Contacts {
	return &Contacts{last: map[string]time.Time{}}
}

// Record notes a message from number at t, keeping the latest time seen.
func (c *Contacts) Record(number string, t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.After(c.last[number]) {
		c.last[number] = t
	}
}

// LastMessage is when number last messaged, if it has since the bot started.
func (c *Contacts) LastMessage(number string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.last[number]
	return t, ok
}
