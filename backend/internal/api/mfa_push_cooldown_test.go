package api

import (
	"testing"
	"time"
)

func TestMfaPushCooldownBlocksWithinWindow(t *testing.T) {
	c := newMfaPushCooldown()
	const userID = "user-1"

	ok, retryAfter := c.allowed(userID)
	if !ok {
		t.Fatal("expected first push to be allowed")
	}
	if retryAfter != 0 {
		t.Fatalf("retryAfter = %v, want 0 when allowed", retryAfter)
	}

	c.recordSent(userID)

	ok, retryAfter = c.allowed(userID)
	if ok {
		t.Fatal("expected a second push within the cooldown window to be blocked")
	}
	if retryAfter <= 0 || retryAfter > mfaPushCooldownFor {
		t.Fatalf("retryAfter = %v, want a positive duration <= %v", retryAfter, mfaPushCooldownFor)
	}
}

func TestMfaPushCooldownIsPerAccount(t *testing.T) {
	c := newMfaPushCooldown()
	c.recordSent("user-a")

	if ok, _ := c.allowed("user-a"); ok {
		t.Fatal("user-a should be in cooldown")
	}
	if ok, _ := c.allowed("user-b"); !ok {
		t.Fatal("user-b's push must not be affected by user-a's cooldown")
	}
}

func TestMfaPushCooldownExpiresAfterWindow(t *testing.T) {
	c := newMfaPushCooldown()
	const userID = "user-2"
	c.recordSent(userID)
	// Simulate the window having already elapsed.
	c.mu.Lock()
	c.lastSent[userID] = time.Now().Add(-mfaPushCooldownFor - time.Second)
	c.mu.Unlock()

	if ok, _ := c.allowed(userID); !ok {
		t.Fatal("expected push to be allowed again once the cooldown window has elapsed")
	}
}
