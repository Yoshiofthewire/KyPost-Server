package classifier

import "testing"

// TestHTTPClientClose confirms the diagnostic log file handles opened by
// NewHTTPClient are released, and that a second Close() call is safe — a
// short-lived client (e.g. an admin connectivity-test request) may be
// closed via defer immediately after use.
func TestHTTPClientClose(t *testing.T) {
	c := NewHTTPClient("http://127.0.0.1:1", "", "/", "", 0)
	if err := c.Close(); err != nil {
		t.Fatalf("first Close(): %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close() should be safe, got: %v", err)
	}
}

func TestResetWarmupStateClearsReadiness(t *testing.T) {
	const key = "test-warmup-key"

	state := getWarmupState(key)
	state.mu.Lock()
	state.ready = true
	state.mu.Unlock()

	if got := getWarmupState(key); !got.ready {
		t.Fatal("test setup invalid: expected warmup state to be marked ready before reset")
	}

	ResetWarmupState()

	if got := getWarmupState(key); got.ready {
		t.Fatal("expected ResetWarmupState to clear cached warmup readiness")
	}
}
