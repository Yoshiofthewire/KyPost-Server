package api

import (
	"sync"
	"time"
)

// sendAsVerificationCooldownFor bounds how often a send-as verification probe
// email may be dispatched for a given (user, candidate address) pair: without
// this, an authenticated user could repeatedly trigger probe emails at any
// third-party address, turning the endpoint into a spam/harassment vector
// against people who never asked to receive anything from this account. This
// does not block the underlying alias record's lifecycle — it only limits how
// often a *new* probe email goes out for the same pair.
const sendAsVerificationCooldownFor = 5 * time.Minute

// sendAsVerificationCooldown is small in-memory, per-(user, candidate
// address) state: after a probe is dispatched for a key, further probes for
// that same pair are suppressed until sendAsVerificationCooldownFor has
// elapsed. Keyed on userID+"|"+normalizedEmail (not bare userID like
// mfaPushCooldown) since the goal is to limit how often any single candidate
// address gets emailed, without penalizing a user who is concurrently
// verifying a different address of their own.
type sendAsVerificationCooldown struct {
	mu       sync.Mutex
	lastSent map[string]time.Time
}

func newSendAsVerificationCooldown() *sendAsVerificationCooldown {
	return &sendAsVerificationCooldown{lastSent: map[string]time.Time{}}
}

// allowed reports whether a verification probe email may be sent for key
// right now, and if not, how much longer until it may.
func (c *sendAsVerificationCooldown) allowed(key string) (ok bool, retryAfter time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, exists := c.lastSent[key]
	if !exists {
		return true, 0
	}
	if remaining := sendAsVerificationCooldownFor - time.Since(last); remaining > 0 {
		return false, remaining
	}
	return true, 0
}

// recordSent marks that a verification probe email was just dispatched for
// key, starting a fresh cooldown window.
func (c *sendAsVerificationCooldown) recordSent(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSent[key] = time.Now()
}
