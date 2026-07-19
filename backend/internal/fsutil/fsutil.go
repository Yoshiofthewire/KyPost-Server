package fsutil

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes payload to path via a temp file + rename so readers
// never observe a partially-written file.
func AtomicWriteFile(path string, payload []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, base+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// LoadJSONFile reads path and json-unmarshals it into a fresh V, passing it
// to apply on success. If the file doesn't exist, it calls onMissing instead
// (nil to treat a missing file as a no-op) — callers use this to distinguish
// first-run seeding (persist an initial empty file) from an in-run refresh
// (keep the current in-memory state). Shared by the per-user JSON stores
// (rules/groups/contacts/mailcache), which otherwise duplicate this
// read-or-seed branch identically for both their load and refresh paths.
func LoadJSONFile[V any](path string, apply func(V), onMissing func() error) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if onMissing != nil {
				return onMissing()
			}
			return nil
		}
		return err
	}
	var v V
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	apply(v)
	return nil
}

// PersistJSONFile marshals v as indented JSON and atomically writes it to
// path with owner-only permissions.
func PersistJSONFile[V any](path string, v V) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWriteFile(path, b, 0o600)
}

// NewUUIDv4 returns a random RFC 4122 version-4 UUID string.
func NewUUIDv4() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
