package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"  debug  ", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"err", slog.LevelError},
		{"garbage", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := parseLevel(tc.in); got != tc.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsSensitiveKey(t *testing.T) {
	positive := []string{
		"token", "Token", "TOKEN",
		"auth_token", "authToken", "x-auth-token",
		"secret", "client_secret",
		"password", "PASSWORD", "user_password", "passwd",
		"authorization",
		"apikey", "ApiKey", "api_key", "api-key", "X-Api-Key",
		"key", "primary_key", "Primary-Key",
	}
	for _, k := range positive {
		if !IsSensitiveKey(k) {
			t.Errorf("expected sensitive: %q", k)
		}
	}

	negative := []string{
		// Built-in slog keys (also short-circuited by redactingReplaceAttr).
		"msg", "time", "level", "source",
		// Common log attrs from our codebase — must not collide.
		"path", "method", "status", "remote", "url", "addr", "tls",
		"auth", // "auth": "enabled" startup line — must not be redacted.
		"latency_ms", "grace", "reason", "error", "login", "source",
		// Innocuous English words that happen to contain "key" but not as a word.
		"monkey", "keyboard", "keep", "turkey", "hockey",
	}
	for _, k := range negative {
		if IsSensitiveKey(k) {
			t.Errorf("expected NOT sensitive: %q", k)
		}
	}
}

func TestRedactionTextHandler(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newHandler(&buf, "text", slog.LevelInfo))
	log.Info("hello",
		"api_key", "topsecret-abc",
		"client_secret", "shhh",
		"user", "alice",
		"auth", "enabled", // must survive — see TestIsSensitiveKey
	)
	out := buf.String()

	if strings.Contains(out, "topsecret-abc") {
		t.Errorf("api_key value leaked: %s", out)
	}
	if strings.Contains(out, "shhh") {
		t.Errorf("client_secret value leaked: %s", out)
	}
	if !strings.Contains(out, RedactedMarker) {
		t.Errorf("expected %q marker, got: %s", RedactedMarker, out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("non-sensitive attr should pass through, got: %s", out)
	}
	if !strings.Contains(out, "auth=enabled") {
		t.Errorf(`"auth": "enabled" must not be redacted, got: %s`, out)
	}
}

func TestRedactionJSONHandler(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newHandler(&buf, "JSON", slog.LevelInfo))
	log.Info("hello",
		"api_key", "topsecret-abc",
		"user", "alice",
	)
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if entry["api_key"] != RedactedMarker {
		t.Errorf("api_key not redacted: %+v", entry)
	}
	if entry["user"] != "alice" {
		t.Errorf("user attr lost: %+v", entry)
	}
	// Built-ins must remain.
	if _, ok := entry["msg"]; !ok {
		t.Error("msg attr missing in JSON output")
	}
	if _, ok := entry["level"]; !ok {
		t.Error("level attr missing in JSON output")
	}
}

func TestRedactionDoesNotTouchBuiltins(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newHandler(&buf, "json", slog.LevelInfo))
	log.Info("a message about a token") // msg contains "token" — must not be redacted
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if entry["msg"] != "a message about a token" {
		t.Errorf("msg attr was rewritten: %+v", entry)
	}
}

func TestLevelFilters(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newHandler(&buf, "text", slog.LevelWarn))
	log.Debug("debug-line")
	log.Info("info-line")
	log.Warn("warn-line")
	log.Error("error-line")
	out := buf.String()
	if strings.Contains(out, "debug-line") {
		t.Errorf("debug should be filtered: %s", out)
	}
	if strings.Contains(out, "info-line") {
		t.Errorf("info should be filtered: %s", out)
	}
	if !strings.Contains(out, "warn-line") {
		t.Error("warn should pass")
	}
	if !strings.Contains(out, "error-line") {
		t.Error("error should pass")
	}
}

func TestFormatDefaultsToText(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newHandler(&buf, "", slog.LevelInfo))
	log.Info("hello")
	out := buf.String()
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("expected text format, got JSON-looking output: %s", out)
	}
	if !strings.Contains(out, "msg=hello") {
		t.Errorf("expected text key=value format, got: %s", out)
	}
}

func TestNewLoggerFromEnv(t *testing.T) {
	// Confirm the env wiring actually flows into the handler.
	t.Setenv(envLogLevel, "warn")
	t.Setenv(envLogFormat, "json")
	var buf bytes.Buffer
	log := newLoggerFromEnv(&buf)
	log.Info("dropped")
	log.Warn("kept", "api_key", "leak-me")
	out := buf.String()
	if strings.Contains(out, "dropped") {
		t.Errorf("info should be filtered at warn level, got: %s", out)
	}
	if !strings.Contains(out, "kept") {
		t.Errorf("warn should pass, got: %s", out)
	}
	if strings.Contains(out, "leak-me") {
		t.Errorf("api_key value leaked even with JSON+env: %s", out)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("expected JSON output, got: %s", out)
	}
}
