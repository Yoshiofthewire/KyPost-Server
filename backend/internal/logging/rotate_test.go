package logging

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// requirePermissionEnforcement skips tests that rely on POSIX permission bits
// actually being enforced: they don't apply on Windows, and they're
// meaningless when running as root, which bypasses file permission checks
// entirely.
func requirePermissionEnforcement(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test relies on POSIX directory permission semantics")
	}
	if os.Geteuid() == 0 {
		t.Skip("permission checks are bypassed when running as root")
	}
}

// TestRotate_NormalRotationHasNoErrors is a sanity check that ordinary,
// unobstructed rotation still works and reports no error.
func TestRotate_NormalRotationHasNoErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	w := newRotatingWriter(path, 10, 2)
	defer w.Close()

	if _, err := w.Write([]byte("0123456789")); err != nil {
		t.Fatalf("initial write failed: %v", err)
	}
	// This write pushes currentSz over maxSize, forcing rotate().
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatalf("write triggering rotation failed: %v", err)
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated backup %s.1 to exist: %v", path, err)
	}
	if w.currentSz != 1 {
		t.Fatalf("currentSz = %d, want 1 (just the byte written after rotation)", w.currentSz)
	}
}

// TestRotate_SurfacesRenameErrors proves that when the final rename of the
// active log file fails, rotate() returns a non-nil error instead of
// silently reporting success. Before the fix, rotate() unconditionally
// returned w.open()'s result, so a failed rename (with a subsequent
// successful reopen of the very same, un-rotated file) was completely
// invisible to callers.
func TestRotate_SurfacesRenameErrors(t *testing.T) {
	requirePermissionEnforcement(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	w := newRotatingWriter(path, 1<<20, 2)
	defer func() {
		_ = os.Chmod(dir, 0o755) // restore so t.TempDir() cleanup can remove it
		w.Close()
	}()

	payload := []byte("hello world\n")
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Remove write permission on the directory (but keep execute/search so
	// path lookups still succeed): this makes Rename/Remove fail with
	// EACCES while re-opening the *existing* file for append still
	// succeeds, since that only needs write permission on the file itself,
	// not on the directory.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}

	err := w.rotate()
	if err == nil {
		t.Fatal("rotate() returned nil error despite a failed rename; the failure was silently swallowed")
	}
	if !strings.Contains(err.Error(), "rename") {
		t.Fatalf("rotate() error = %q, want it to mention the failed rename", err.Error())
	}
}

// TestRotate_PreservesSizeOnFailedRename proves that when the final rename
// fails, currentSz is set to the file's real on-disk size rather than being
// reset to 0. Before the fix, rotate() unconditionally set currentSz = 0; if
// the subsequent reopen also failed (as forced here), nothing ever
// corrected that wrong value, so the writer would believe the file was
// empty even though it still held all its original, un-rotated bytes —
// letting the file grow without bound since it always looked "small enough"
// not to rotate again.
func TestRotate_PreservesSizeOnFailedRename(t *testing.T) {
	requirePermissionEnforcement(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	w := newRotatingWriter(path, 1<<20, 2)
	defer func() {
		_ = os.Chmod(dir, 0o755)
		_ = os.Chmod(path, 0o644)
		w.Close()
	}()

	payload := []byte("hello world, this is the un-rotated payload\n")
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	realSize := w.currentSz
	if realSize == 0 {
		t.Fatal("test setup bug: expected a non-zero size before rotating")
	}

	// Block the directory (rename/remove fail with EACCES) AND make the
	// file itself unwritable (the reopen inside rotate()'s call to open()
	// fails too, so nothing overwrites currentSz for us afterward).
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatalf("chmod file: %v", err)
	}

	err := w.rotate()
	if err == nil {
		t.Fatal("rotate() returned nil error despite failed rename and failed reopen")
	}

	if w.currentSz == 0 {
		t.Fatalf("currentSz reset to 0 after failed rotation; want it to reflect the real file size (%d)", realSize)
	}
	if w.currentSz != realSize {
		t.Fatalf("currentSz = %d, want %d (the real on-disk size)", w.currentSz, realSize)
	}
}

// TestRotate_JoinsMultipleErrors proves that when several steps of rotation
// fail at once, rotate() surfaces all of them (via errors.Join) rather than
// only the last one, so none are silently dropped.
func TestRotate_JoinsMultipleErrors(t *testing.T) {
	requirePermissionEnforcement(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	// maxFiles=3 so the backup-rotation loop (path.2 -> path.3) also runs
	// and can independently fail alongside the main rename.
	w := newRotatingWriter(path, 1<<20, 3)
	defer func() {
		_ = os.Chmod(dir, 0o755)
		w.Close()
	}()

	if _, err := w.Write([]byte("seed\n")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := os.WriteFile(path+".2", []byte("old backup\n"), 0o644); err != nil {
		t.Fatalf("seed backup file: %v", err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}

	err := w.rotate()
	if err == nil {
		t.Fatal("rotate() returned nil error despite multiple failing filesystem operations")
	}

	joined, ok := interface{}(err).(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("rotate() error does not support multi-error unwrapping (errors.Join): %v", err)
	}
	if got := len(joined.Unwrap()); got < 2 {
		t.Fatalf("expected at least 2 joined errors (backup rename + main rename), got %d: %v", got, err)
	}

	// Sanity: errors.Is/As-style inspection also works through the joined error.
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) {
		t.Fatalf("expected a wrapped *os.LinkError (from the failed os.Rename) to be reachable via errors.As, got: %v", err)
	}
}
