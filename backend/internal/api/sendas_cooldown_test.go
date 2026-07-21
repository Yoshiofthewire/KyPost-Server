package api

import (
	"testing"
	"time"
)

func TestSendAsVerificationCooldownBlocksWithinWindow(t *testing.T) {
	c := newSendAsVerificationCooldown()
	const key = "user-1|alice@example.com"

	ok, retryAfter := c.allowed(key)
	if !ok {
		t.Fatal("expected first verification probe to be allowed")
	}
	if retryAfter != 0 {
		t.Fatalf("retryAfter = %v, want 0 when allowed", retryAfter)
	}

	c.recordSent(key)

	ok, retryAfter = c.allowed(key)
	if ok {
		t.Fatal("expected a second probe within the cooldown window to be blocked")
	}
	if retryAfter <= 0 || retryAfter > sendAsVerificationCooldownFor {
		t.Fatalf("retryAfter = %v, want a positive duration <= %v", retryAfter, sendAsVerificationCooldownFor)
	}
}

// TestSendAsVerificationCooldownIsPerKey demonstrates the reason this type is
// keyed on userID+"|"+email rather than bare userID like mfaPushCooldown: the
// same user verifying a second alias must not be blocked by the cooldown
// already in effect for their first alias.
func TestSendAsVerificationCooldownIsPerKey(t *testing.T) {
	c := newSendAsVerificationCooldown()
	const aliceKey = "user-a|alice@example.com"
	const bobKey = "user-a|bob@example.com"

	c.recordSent(aliceKey)

	if ok, _ := c.allowed(aliceKey); ok {
		t.Fatal("user-a's probe to alice@example.com should be in cooldown")
	}
	if ok, _ := c.allowed(bobKey); !ok {
		t.Fatal("user-a's probe to a different candidate address (bob@example.com) must not be affected by the cooldown on alice@example.com")
	}
}

func TestSendAsVerificationCooldownExpiresAfterWindow(t *testing.T) {
	c := newSendAsVerificationCooldown()
	const key = "user-2|carol@example.com"
	c.recordSent(key)
	// Simulate the window having already elapsed.
	c.mu.Lock()
	c.lastSent[key] = time.Now().Add(-sendAsVerificationCooldownFor - time.Second)
	c.mu.Unlock()

	if ok, _ := c.allowed(key); !ok {
		t.Fatal("expected probe to be allowed again once the cooldown window has elapsed")
	}
}
