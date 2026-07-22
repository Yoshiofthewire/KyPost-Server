package api

import (
	"sync"
	"time"
)

// nativePairingNonceSweepThreshold bounds how large consumedNativePairingNonces
// can grow before a housekeeping sweep of expired entries runs.
const nativePairingNonceSweepThreshold = 10_000

// consumedNativePairingNonces is small in-memory, mutex-guarded state
// tracking which native-device-pairing token nonces have already been
// redeemed. Scoped deliberately to the native-pairing flow only (90-second
// token TTL) — the same signed-token mechanism also backs PGP QR pairing and
// pickup links at longer TTLs, but those either have their own one-time-use
// semantics already (pickup's PickupStore.View tombstones on first read) or
// aren't in scope here, so this type is never consulted from those paths.
//
// In-memory-only (no disk persistence) is a deliberate, low-risk
// simplification: a process restart within the 90-second window is a
// narrow edge case, not a meaningful reintroduction of the reuse bug this
// closes.
type consumedNativePairingNonces struct {
	mu   sync.Mutex
	seen map[string]time.Time // nonce -> expiry
}

func newConsumedNativePairingNonces() *consumedNativePairingNonces {
	return &consumedNativePairingNonces{seen: map[string]time.Time{}}
}

// consume atomically checks whether nonce has already been redeemed and, if
// not, records it with the given ttl and returns true. A second call with
// the same nonce (replay) returns false. Doing the check and the insert
// under one lock closes the same TOCTOU window ConsumeTOTPStep's doc-comment
// (mfa.go) describes for its own equivalent operation.
func (c *consumedNativePairingNonces) consume(nonce string, ttl time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.seen) >= nativePairingNonceSweepThreshold {
		now := time.Now()
		for k, exp := range c.seen {
			if now.After(exp) {
				delete(c.seen, k)
			}
		}
	}

	if exp, exists := c.seen[nonce]; exists && time.Now().Before(exp) {
		return false
	}
	c.seen[nonce] = time.Now().Add(ttl)
	return true
}
