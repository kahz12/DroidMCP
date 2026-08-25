// Package logger provides a structured logging wrapper around slog.
// It redirects all logs to stderr to keep stdout clean for potential
// protocol communication and applies two cross-cutting concerns at
// configuration time:
//
//   - Level and format come from the environment
//     (DROIDMCP_LOG_LEVEL, DROIDMCP_LOG_FORMAT) so operators can tune
//     verbosity and pick a JSON handler in production without changing
//     code.
//   - Attribute keys that look like credentials are redacted via
//     slog.HandlerOptions.ReplaceAttr, so accidental
//     logger.Info("...", "api_key", value) never reaches the sink.
package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Env keys recognised by the logger. Empty / unknown values fall back to
// the defaults (info level, text handler).
const (
	envLogLevel  = "DROIDMCP_LOG_LEVEL"
	envLogFormat = "DROIDMCP_LOG_FORMAT"
)

// redactedMarker is the placeholder that replaces sensitive values in
// logged attributes.
const redactedMarker = "[REDACTED]"

// Log is the global structured logger instance.
var Log *slog.Logger

func init() {
	Log = newLoggerFromEnv(os.Stderr)
}

func newLoggerFromEnv(w io.Writer) *slog.Logger {
	level := parseLevel(os.Getenv(envLogLevel))
	format := os.Getenv(envLogFormat)
	return slog.New(newHandler(w, format, level))
}

// newHandler builds a slog.Handler honouring the requested format ("json"
// case-insensitively selects JSON, anything else picks text) and level,
// always wired through the sensitive-key redactor.
func newHandler(w io.Writer, format string, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactingReplaceAttr,
	}
	if strings.EqualFold(strings.TrimSpace(format), "json") {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// parseLevel maps a string to slog.Level. Unknown / empty values default to
// info. "warning" and "err" are accepted as aliases for the common typos.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// redactingReplaceAttr is plugged into HandlerOptions. For each non-builtin
// attribute, if the key looks like it carries a credential the value is
// replaced with redactedMarker. Built-in attrs (time/level/msg/source) and
// group attrs are passed through untouched so the log structure stays
// intact.
func redactingReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		switch a.Key {
		case slog.TimeKey, slog.LevelKey, slog.MessageKey, slog.SourceKey:
			return a
		}
	}
	if a.Value.Kind() == slog.KindGroup {
		return a
	}
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redactedMarker)
	}
	return a
}

// sensitiveSubstrings is a small, intentionally narrow set: matches that
// almost certainly indicate a credential. We deliberately do NOT include
// "auth" alone because it collides with non-sensitive attrs like the
// "auth: enabled" startup line in core/server.go.
var sensitiveSubstrings = []string{
	"token",
	"secret",
	"password",
	"passwd",
	"authorization",
	"apikey",
	"api_key",
	"api-key",
}

// isSensitiveKey reports whether the given slog attribute key likely
// carries a credential. It runs a case-insensitive substring check against
// sensitiveSubstrings and an additional word-boundary check for the
// standalone token "key" so "api_key" / "x-api-key" / "primary_key" are
// caught while "monkey" / "keyboard" / "keep" / "ApiKey" too (camelCase is
// handled by lowercasing then checking "apikey" substring).
func isSensitiveKey(key string) bool {
	lk := strings.ToLower(key)
	for _, s := range sensitiveSubstrings {
		if strings.Contains(lk, s) {
			return true
		}
	}
	return containsWord(lk, "key")
}

// containsWord reports whether word occurs in s bounded on either side by
// the start/end of the string or a non-alphanumeric character. The check
// is plain ASCII because slog attribute keys in this codebase are ASCII.
func containsWord(s, word string) bool {
	idx := 0
	for {
		i := strings.Index(s[idx:], word)
		if i < 0 {
			return false
		}
		i += idx
		end := i + len(word)
		leftOK := i == 0 || !isAlnum(s[i-1])
		rightOK := end == len(s) || !isAlnum(s[end])
		if leftOK && rightOK {
			return true
		}
		idx = i + 1
	}
}

func isAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// Info logs an informational message with optional key-value pairs.
func Info(msg string, args ...any) { Log.Info(msg, args...) }

// Warn logs a warning message with optional key-value pairs.
func Warn(msg string, args ...any) { Log.Warn(msg, args...) }

// Error logs an error message with the "error" key and optional context.
func Error(msg string, err error, args ...any) {
	args = append(args, "error", err)
	Log.Error(msg, args...)
}

// Fatal logs an error and terminates the process. Use sparingly.
func Fatal(msg string, err error, args ...any) {
	Error(msg, err, args...)
	os.Exit(1)
}
