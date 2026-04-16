package logger

import (
	"log/slog"
	"os"
)

var Log *slog.Logger

func init() {
	Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func Info(msg string, args ...any) {
	Log.Info(msg, args...)
}

func Error(msg string, err error, args ...any) {
	args = append(args, "error", err)
	Log.Error(msg, args...)
}

func Fatal(msg string, err error, args ...any) {
	Error(msg, err, args...)
	os.Exit(1)
}
