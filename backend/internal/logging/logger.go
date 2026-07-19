package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

type Logger struct {
	logger *slog.Logger
	writer *rotatingWriter
}

func New(logDir string) (*Logger, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	w := newRotatingWriter(filepath.Join(logDir, "app.log"), 16*1024*1024, 8)
	mw := io.MultiWriter(os.Stdout, w)
	return &Logger{
		logger: slog.New(slog.NewTextHandler(mw, nil)),
		writer: w,
	}, nil
}

func (l *Logger) Close() error {
	return l.writer.Close()
}

func (l *Logger) Info(msg string, kv ...string) {
	l.logger.Info(msg, stringsToArgs(kv)...)
}

func (l *Logger) Error(msg string, kv ...string) {
	l.logger.Error(msg, stringsToArgs(kv)...)
}

// stringsToArgs adapts the Logger.Info/Error flat-string-pairs API (kept for
// caller convenience — every call site already has string values) onto
// slog's ...any args.
func stringsToArgs(kv []string) []any {
	args := make([]any, len(kv))
	for i, v := range kv {
		args[i] = v
	}
	return args
}
