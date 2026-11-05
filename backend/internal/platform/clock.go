package platform

import (
	"sync"
	"time"
)

// Clock is the only source of "now" for application code (ADR 0014), so tests
// control time without sleeps.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the real clock. It returns UTC: time is UTC internally and
// converted to Asia/Taipei only for display (I18N-004).
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

// ManualClock is a Clock for tests; it only moves when told to.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewManualClock(t time.Time) *ManualClock { return &ManualClock{now: t.UTC()} }

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t.UTC()
}

func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
