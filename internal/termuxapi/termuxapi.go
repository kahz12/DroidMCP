// Package termuxapi recognises the error documents Termux:API prints on stdout.
// The termux-* wrappers exit 0 even then, so a server that only checks the
// exit status would report a missing permission as a successful call — or, for
// termux-sms-send, claim it sent a message that never left. Every server that
// passes Termux:API output on checks it here first.
package termuxapi

import (
	"encoding/json"
	"fmt"
	"strings"
)

// errorKeys are the fields Termux:API reports a failure in: {"error": "…"}
// for a missing permission (e.g. "Please grant the following permission to use
// this command: android.permission.READ_SMS") and {"API_ERROR": "…"} for an
// endpoint that failed (termux-location's "Failed to get location").
var errorKeys = []string{"error", "API_ERROR"}

// permissionHint says where a missing permission is granted; the API's own
// message names the permission but not that.
const permissionHint = "grant it to the Termux:API app in Android Settings → Apps → Termux:API → Permissions, then retry"

// Failure reports whether stdout is a Termux:API error document — a JSON
// object whose only field is "error" or "API_ERROR", holding a non-empty
// string — and returns the message a tool should fail with: the command, the
// API's message and, for a permission error, where to grant it. A result
// object, an array, an object with other fields or plain text is not an error
// document, so a real result is never mistaken for one.
func Failure(command, stdout string) (string, bool) {
	s := strings.TrimSpace(stdout)
	if !strings.HasPrefix(s, "{") {
		return "", false
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(s), &doc) != nil || len(doc) != 1 {
		return "", false
	}
	for _, key := range errorKeys {
		raw, ok := doc[key]
		if !ok {
			continue
		}
		var msg string
		if json.Unmarshal(raw, &msg) != nil || strings.TrimSpace(msg) == "" {
			return "", false
		}
		out := fmt.Sprintf("%s: %s", command, strings.TrimSpace(msg))
		if strings.Contains(strings.ToLower(msg), "permission") {
			out += " (" + permissionHint + ")"
		}
		return out, true
	}
	return "", false
}
