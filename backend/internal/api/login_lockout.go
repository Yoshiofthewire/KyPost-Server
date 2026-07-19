package api

import (
	"sync"
	"time"
)

// loginMaxFailures/loginLockoutFor implement a three-strikes, 15-minute
// cooldown on password login: after loginMaxFailures failed attempts for a
// given username, further attempts for that exact username are rejected
// until loginLockoutFor has elapsed. This is independent of whether the
// username actually exists — see loginLockout.allowed — so it can't be used
// to distinguish valid from invalid usernames by lockout behavior.
const (
	loginMaxFailures = 3
	loginLockoutFor  = 15 * time.Minute

	// loginLockoutSweepThreshold bounds how large loginLockout.entries can
	// grow before a housekeeping sweep runs. An attacker submitting a stream
	// of distinct, nonexistent usernames each gets its own entry that never
	// reaches the lockout threshold and is otherwise never removed —
	// unbounded memory growth over a sustained attack. Sweeping out every
	// not-currently-locked entry once the map gets this large keeps memory
	// bounded without a background goroutine; legitimate locked-out entries
	// (the ones actually worth remembering) are untouched.
	loginLockoutSweepThreshold = 10_000
)

type loginLockoutEntry struct {
	failures    int
	lockedUntil time.Time
}

// loginLockout is small in-memory, per-username state guarding
// handleLogin. It intentionally lives outside Server.sessions/mu — it's
// unrelated state with its own, much smaller lock scope.
type loginLockout struct {
	mu      sync.Mutex
	entries map[string]*loginLockoutEntry
}

func newLoginLockout() *loginLockout {
	return &loginLockout{entries: map[string]*loginLockoutEntry{}}
}

// allowed reports whether username may attempt a login right now. When
// false, retryAfter is how much longer the lockout has to run.
func (l *loginLockout) allowed(username string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, exists := l.entries[username]
	if !exists {
		return true, 0
	}
	if remaining := time.Until(e.lockedUntil); remaining > 0 {
		return false, remaining
	}
	return true, 0
}

// recordFailure counts one failed attempt for username, locking it out for
// loginLockoutFor once it reaches loginMaxFailures. A lockout that has
// already expired resets the strike count first, so failures don't
// accumulate forever across unrelated attempts long after the last lockout.
func (l *loginLockout) recordFailure(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) >= loginLockoutSweepThreshold {
		now := time.Now()
		for k, e := range l.entries {
			if e.lockedUntil.IsZero() || !now.Before(e.lockedUntil) {
				delete(l.entries, k)
			}
		}
	}
	e, exists := l.entries[username]
	if !exists {
		e = &loginLockoutEntry{}
		l.entries[username] = e
	} else if !e.lockedUntil.IsZero() && !time.Now().Before(e.lockedUntil) {
		e.failures = 0
		e.lockedUntil = time.Time{}
	}
	e.failures++
	if e.failures >= loginMaxFailures {
		e.lockedUntil = time.Now().Add(loginLockoutFor)
	}
}

// recordSuccess clears any strike history for username, so a successful
// login always starts the next set of attempts with a clean slate.
func (l *loginLockout) recordSuccess(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, username)
}
