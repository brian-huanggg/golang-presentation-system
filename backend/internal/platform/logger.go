package platform

import (
	"io"
	"log/slog"
	"strings"
)

// NewLogger returns a JSON slog logger (Architecture 4). Callers must never log
// secrets; Config's own formatting redacts them.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// ParseLogLevel maps debug|info|warn|error (case-insensitive) to a slog.Level.
func ParseLogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "", "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}
