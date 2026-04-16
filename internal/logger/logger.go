// Package logger provides a structured logging wrapper around slog.
// It redirects all logs to stderr to keep stdout clean for potential protocol communication.
package logger

import (
	"log/slog"
	"os"
)

// Log is the global structured logger instance.
var Log *slog.Logger

func init() {
	// We use TextHandler for better readability in terminal environments (Termux).
	Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
}

// Info logs an informational message with optional key-value pairs.
func Info(msg string, args ...any) {
	Log.Info(msg, args...)
}

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
