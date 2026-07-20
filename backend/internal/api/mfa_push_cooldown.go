package api

import (
	"sync"
	"time"
)

// mfaPushCooldownFor bounds how often a login can trigger an actual MFA push
// notification for a given account: without this, an attacker who already
// holds a valid password can mint an unlimited number of real MFA challenges
// by calling login repeatedly (each successful password check clears
// loginLockout above), bombarding the user's approver devices with "Approve
// sign-in" pushes until one gets tapped out of habit or annoyance — the
// "MFA fatigue" pattern used in the 2022 Uber breach. This does not block
// login or challenge creation itself (a user who mistyped a TOTP code must
// still be able to retry), it only limits how often the push notification
// fans out, independent of how many challenges get created underneath it.
const mfaPushCooldownFor = 5 * time.Minute

// mfaPushCooldown is small in-memory, per-account state: after a push is
// dispatched for userID, further pushes for that account are suppressed
// until mfaPushCooldownFor has elapsed. Keyed on the account's user ID
// (not username+IP like loginLockout) since the whole point is to limit
// delivery to that account's devices regardless of where login attempts
// against it originate from.
type mfaPushCooldown struct {
	mu       sync.Mutex
	lastSent map[string]time.Time
}

func newMfaPushCooldown() *mfaPushCooldown {
	return &mfaPushCooldown{lastSent: map[string]time.Time{}}
}

// allowed reports whether a push notification may be sent to userID right
// now, and if not, how much longer until it may.
func (c *mfaPushCooldown) allowed(userID string) (ok bool, retryAfter time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, exists := c.lastSent[userID]
	if !exists {
		return true, 0
	}
	if remaining := mfaPushCooldownFor - time.Since(last); remaining > 0 {
		return false, remaining
	}
	return true, 0
}

// recordSent marks that a push notification was just dispatched to userID,
// starting a fresh cooldown window.
func (c *mfaPushCooldown) recordSent(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSent[userID] = time.Now()
}
