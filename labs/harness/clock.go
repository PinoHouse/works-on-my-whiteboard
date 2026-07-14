package harness

import (
	"fmt"
	"sync"
	"time"
)

type manualClock struct {
	mu  sync.RWMutex
	now time.Time
}

func newManualClock(start time.Time) *manualClock {
	return &manualClock{now: start.UTC()}
}

func (c *manualClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *manualClock) advance(next time.Time) error {
	next = next.UTC()
	c.mu.Lock()
	defer c.mu.Unlock()
	if next.Before(c.now) {
		return fmt.Errorf("logical clock cannot move backward from %s to %s", c.now.Format(time.RFC3339Nano), next.Format(time.RFC3339Nano))
	}
	if next.Equal(c.now) {
		return nil
	}
	c.now = next
	return nil
}
