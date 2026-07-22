package api

import (
	"sync"
	"time"
)

// classifierTestCooldownFor bounds how often one admin may fire a
// connectivity-test request against the shared classifier/Ollama instance:
// handleClassifierTest builds its own ad-hoc classifier client rather than
// reusing the server's shared instance (to always reflect the currently
// saved config), which means it bypasses that shared client's own
// serialization/pacing against live poller traffic. This cooldown is the
// narrow substitute — it can't prevent a test request from racing a real
// classification, but it does prevent an admin (or a compromised admin
// session) from firing unlimited concurrent test requests that pile up
// unpaced against the same backend.
const classifierTestCooldownFor = 10 * time.Second

// classifierTestCooldown is small in-memory, per-admin state, keyed on the
// admin's user ID.
type classifierTestCooldown struct {
	mu       sync.Mutex
	lastSent map[string]time.Time
}

func newClassifierTestCooldown() *classifierTestCooldown {
	return &classifierTestCooldown{lastSent: map[string]time.Time{}}
}

// tryConsume atomically checks whether a test request may proceed for key
// right now and, if so, records it in the same critical section.
func (c *classifierTestCooldown) tryConsume(key string) (ok bool, retryAfter time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if last, exists := c.lastSent[key]; exists {
		if remaining := classifierTestCooldownFor - time.Since(last); remaining > 0 {
			return false, remaining
		}
	}
	c.lastSent[key] = time.Now()
	return true, 0
}
