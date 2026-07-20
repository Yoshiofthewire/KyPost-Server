package cryptutil

import (
	"path/filepath"
	"sync"
	"testing"
)

// TestLoadOrCreateKeyConcurrentFirstUseReturnsOneKey verifies the fix for
// security-audit run-2's master-key generation race: concurrent first-use
// callers racing the same not-yet-created key path must all observe the same
// persisted key, never a mix of distinct in-memory keys where only one
// actually got written to disk (which silently and permanently orphans every
// other caller's already-sealed data).
func TestLoadOrCreateKeyConcurrentFirstUseReturnsOneKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared-master.key")

	const n = 50
	keys := make([][]byte, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	barrier := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-barrier
			keys[i], errs[i] = LoadOrCreateKey(path)
		}(i)
	}
	close(barrier)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: LoadOrCreateKey error: %v", i, err)
		}
	}

	persisted, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey after concurrent creation: %v", err)
	}

	for i, k := range keys {
		if string(k) != string(persisted) {
			t.Fatalf("goroutine %d returned a key that does not match the persisted key — first-use generation raced", i)
		}
	}
}
