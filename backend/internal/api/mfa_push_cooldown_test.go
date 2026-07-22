package api

import (
	"sync"
	"testing"
	"time"
)

func TestMfaPushCooldownBlocksWithinWindow(t *testing.T) {
	c := newMfaPushCooldown()
	const userID = "user-1"

	ok, retryAfter := c.tryConsume(userID)
	if !ok {
		t.Fatal("expected first push to be allowed")
	}
	if retryAfter != 0 {
		t.Fatalf("retryAfter = %v, want 0 when allowed", retryAfter)
	}

	ok, retryAfter = c.tryConsume(userID)
	if ok {
		t.Fatal("expected a second push within the cooldown window to be blocked")
	}
	if retryAfter <= 0 || retryAfter > mfaPushCooldownFor {
		t.Fatalf("retryAfter = %v, want a positive duration <= %v", retryAfter, mfaPushCooldownFor)
	}
}

func TestMfaPushCooldownIsPerAccount(t *testing.T) {
	c := newMfaPushCooldown()
	c.tryConsume("user-a")

	if ok, _ := c.tryConsume("user-a"); ok {
		t.Fatal("user-a should be in cooldown")
	}
	if ok, _ := c.tryConsume("user-b"); !ok {
		t.Fatal("user-b's push must not be affected by user-a's cooldown")
	}
}

func TestMfaPushCooldownExpiresAfterWindow(t *testing.T) {
	c := newMfaPushCooldown()
	const userID = "user-2"
	c.tryConsume(userID)
	// Simulate the window having already elapsed.
	c.mu.Lock()
	c.lastSent[userID] = time.Now().Add(-mfaPushCooldownFor - time.Second)
	c.mu.Unlock()

	if ok, _ := c.tryConsume(userID); !ok {
		t.Fatal("expected push to be allowed again once the cooldown window has elapsed")
	}
}

// TestMfaPushCooldownTryConsumeIsAtomicUnderRace fires many concurrent
// tryConsume calls for the same account and asserts exactly one succeeds —
// this is the exact TOCTOU the old separate allowed()+recordSent() calls
// were vulnerable to.
func TestMfaPushCooldownTryConsumeIsAtomicUnderRace(t *testing.T) {
	c := newMfaPushCooldown()
	const userID = "user-race"
	const n = 50

	var wg sync.WaitGroup
	barrier := make(chan struct{})
	results := make([]bool, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-barrier
			ok, _ := c.tryConsume(userID)
			results[idx] = ok
		}(i)
	}
	close(barrier)
	wg.Wait()

	allowedCount := 0
	for _, ok := range results {
		if ok {
			allowedCount++
		}
	}
	if allowedCount != 1 {
		t.Fatalf("expected exactly 1 of %d concurrent tryConsume calls to succeed, got %d", n, allowedCount)
	}
}
