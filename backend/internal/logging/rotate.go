package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type rotatingWriter struct {
	mu        sync.Mutex
	path      string
	maxSize   int64
	maxFiles  int
	current   *os.File
	currentSz int64
}

func newRotatingWriter(path string, maxSize int64, maxFiles int) *rotatingWriter {
	w := &rotatingWriter{path: path, maxSize: maxSize, maxFiles: maxFiles}
	_ = w.open()
	return w
}

// NewRotatingWriter returns a size- and count-bounded rotating file writer,
// safe for concurrent use. Exported so other packages that need their own
// timestamped/prefixed line format (rather than Logger's structured
// key=value output) can still share this rotation implementation instead of
// hand-rolling their own.
func NewRotatingWriter(path string, maxSize int64, maxFiles int) io.WriteCloser {
	return newRotatingWriter(path, maxSize, maxFiles)
}

func (w *rotatingWriter) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.current = f
	w.currentSz = st.Size()
	return nil
}

func (w *rotatingWriter) rotate() error {
	var errs []error

	if w.current != nil {
		if err := w.current.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", w.path, err))
		}
	}
	for i := w.maxFiles - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", w.path, i)
		dst := fmt.Sprintf("%s.%d", w.path, i+1)
		if i == w.maxFiles-1 {
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("remove %s: %w", dst, err))
			}
		}
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				errs = append(errs, fmt.Errorf("rename %s to %s: %w", src, dst, err))
			}
		}
	}

	w.current = nil
	if _, err := os.Stat(w.path); err == nil {
		if err := os.Rename(w.path, fmt.Sprintf("%s.1", w.path)); err != nil {
			errs = append(errs, fmt.Errorf("rename %s to %s.1: %w", w.path, w.path, err))
			// The active file wasn't actually rotated away, so it kept
			// whatever bytes it already had. Re-stat it to learn the real
			// size instead of assuming rotation succeeded and resetting to
			// zero — otherwise the size bound silently stops being
			// enforced and the file grows without limit.
			if st, statErr := os.Stat(w.path); statErr == nil {
				w.currentSz = st.Size()
			} else {
				errs = append(errs, fmt.Errorf("stat %s after failed rotation: %w", w.path, statErr))
			}
		} else {
			w.currentSz = 0
		}
	} else {
		w.currentSz = 0
	}

	if err := w.open(); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (w *rotatingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.current == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.currentSz+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err = w.current.Write(p)
	w.currentSz += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	if w.current == nil {
		return nil
	}
	return w.current.Close()
}

var _ io.WriteCloser = (*rotatingWriter)(nil)
