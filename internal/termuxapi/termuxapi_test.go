package termuxapi

import (
	"strings"
	"testing"
)

func TestFailureRecognisesErrorDocuments(t *testing.T) {
	cases := []struct {
		name, stdout string
		want         []string // substrings of the message
	}{
		{
			name:   "missing permission",
			stdout: `{"error":"Please grant the following permission to use this command: android.permission.READ_SMS"}`,
			want:   []string{"termux-sms-list: Please grant", "READ_SMS", "Android Settings → Apps → Termux:API → Permissions"},
		},
		{
			name:   "endpoint failure",
			stdout: "{\n  \"API_ERROR\": \"Failed to get location\"\n}\n",
			want:   []string{"termux-sms-list: Failed to get location"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, ok := Failure("termux-sms-list", tc.stdout)
			if !ok {
				t.Fatalf("not recognised as an error document: %s", tc.stdout)
			}
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("message %q does not contain %q", msg, w)
				}
			}
		})
	}
}

func TestFailureOnlyAddsPermissionHintForPermissions(t *testing.T) {
	msg, _ := Failure("termux-location", `{"API_ERROR":"Failed to get location"}`)
	if strings.Contains(msg, "Permissions") {
		t.Errorf("non-permission error got the permission hint: %q", msg)
	}
}

func TestFailureIgnoresResults(t *testing.T) {
	for _, stdout := range []string{
		``,
		`[]`,
		`[{"error":"x"}]`, // an array, even of error-shaped objects
		`{"percentage":67,"status":"DISCHARGING"}`, // a normal result object
		`{"error":"x","percentage":67}`,            // an error field among real fields
		`{"error":""}`,                             // no message
		`{"error":42}`,                             // not a string
		`{"API_ERROR":null}`,                       // not a string
		`error: something`,                         // plain text
		`{not json`,
	} {
		if msg, ok := Failure("termux-x", stdout); ok {
			t.Errorf("%q wrongly recognised as an error document: %q", stdout, msg)
		}
	}
}
