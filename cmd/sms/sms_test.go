package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// sampleMessages is what the fake termux-sms-list emits.
const sampleMessages = `[
	{"threadid":1,"type":"inbox","read":true,"number":"+1 (555) 123-4567","received":"2026-07-20 10:00","body":"Your code is 123456"},
	{"threadid":2,"type":"inbox","read":false,"number":"555-987-6543","received":"2026-07-21 09:00","body":"Lunch tomorrow?"},
	{"threadid":3,"type":"sent","read":true,"number":"+1 (555) 123-4567","received":"2026-07-21 11:00","body":"Thanks, got it"}
]`

func fakeSMSList(t *testing.T, body string) func() string {
	dir := fakeBinDir(t)
	argv, _ := capture(t, dir)
	fakeBin(t, dir, binSMSList, `printf '%s\n' "$@" > "$SMS_ARGS_FILE"; cat <<'EOF'`+"\n"+body+"\nEOF")
	return argv
}

func TestListSMSPassthroughAndArgs(t *testing.T) {
	argv := fakeSMSList(t, sampleMessages)

	got, isErr := resultText(t, mustCall(t, handleListSMS, map[string]any{"type": "inbox", "limit": 5.0, "offset": 2.0}))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil || len(out) != 3 {
		t.Fatalf("expected the JSON array to pass through, got %s (err %v)", got, err)
	}
	for _, want := range []string{"-t\ninbox", "-l\n5", "-o\n2"} {
		if !strings.Contains(argv(), want) {
			t.Errorf("argv missing %q:\n%s", want, argv())
		}
	}
}

func TestListSMSInvalidType(t *testing.T) {
	// Type is validated before any exec, so no fake binary is needed.
	if _, isErr := resultText(t, mustCall(t, handleListSMS, map[string]any{"type": "spam"})); !isErr {
		t.Error("unknown type should error")
	}
}

func TestListSMSDefaultsAndClamp(t *testing.T) {
	argv := fakeSMSList(t, "[]")

	// No type → all; huge limit → clamped to maxLimit; negative offset → 0.
	if _, isErr := resultText(t, mustCall(t, handleListSMS, map[string]any{"limit": 99999.0, "offset": -5.0})); isErr {
		t.Fatal("valid call should succeed")
	}
	for _, want := range []string{"-t\nall", "-l\n500", "-o\n0"} {
		if !strings.Contains(argv(), want) {
			t.Errorf("argv missing %q:\n%s", want, argv())
		}
	}
}

func TestSearchSMSByBody(t *testing.T) {
	fakeSMSList(t, sampleMessages)

	got, _ := resultText(t, mustCall(t, handleSearchSMS, map[string]any{"query": "code"}))
	var out searchResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Count != 1 || out.Messages[0]["threadid"].(float64) != 1 {
		t.Errorf("body search failed: %s", got)
	}
}

func TestSearchSMSByNumberIgnoresFormatting(t *testing.T) {
	fakeSMSList(t, sampleMessages)

	got, _ := resultText(t, mustCall(t, handleSearchSMS, map[string]any{"number": "5551234567"}))
	var out searchResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Count != 2 {
		t.Errorf("expected 2 messages with that number, got %d: %s", out.Count, got)
	}
}

func TestSearchSMSBodyAndNumberAreAnded(t *testing.T) {
	fakeSMSList(t, sampleMessages)

	got, _ := resultText(t, mustCall(t, handleSearchSMS, map[string]any{"query": "thanks", "number": "5551234567"}))
	var out searchResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Count != 1 || out.Messages[0]["threadid"].(float64) != 3 {
		t.Errorf("combined filter should match only the sent 'Thanks' message: %s", got)
	}
}

func TestSearchSMSRequiresCriterion(t *testing.T) {
	if _, isErr := resultText(t, mustCall(t, handleSearchSMS, nil)); !isErr {
		t.Error("missing query and number should error")
	}
}

func TestSendSMSWiring(t *testing.T) {
	dir := fakeBinDir(t)
	argv, stdin := capture(t, dir)
	fakeBin(t, dir, binSMSSend, recordArgv)

	got, isErr := resultText(t, mustCall(t, handleSendSMS, map[string]any{
		"number": "+15551234567", "text": "hello there", "sim_slot": 1.0,
	}))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out sendResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if !out.Sent || len(out.Recipients) != 1 || out.Recipients[0] != "+15551234567" || out.SimSlot == nil || *out.SimSlot != 1 {
		t.Errorf("got %+v", out)
	}
	// Recipients go via -n, the slot via -s, and the body ONLY on stdin.
	if a := argv(); !strings.Contains(a, "-n\n+15551234567") || !strings.Contains(a, "-s\n1") {
		t.Errorf("argv missing recipient/slot flags:\n%s", a)
	}
	if a := argv(); strings.Contains(a, "hello there") {
		t.Errorf("body must NOT appear in argv:\n%s", a)
	}
	if s := strings.TrimSpace(stdin()); s != "hello there" {
		t.Errorf("body should be delivered on stdin, got %q", s)
	}
}

func TestSendSMSMultipleRecipients(t *testing.T) {
	dir := fakeBinDir(t)
	argv, _ := capture(t, dir)
	fakeBin(t, dir, binSMSSend, recordArgv)

	got, isErr := resultText(t, mustCall(t, handleSendSMS, map[string]any{
		"number": " +15551112222 , 5553334444 ", "text": "hi",
	}))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out sendResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(out.Recipients) != 2 {
		t.Fatalf("expected 2 recipients, got %+v", out.Recipients)
	}
	// Cleaned and comma-joined with no spaces (so the wrapper's split is safe).
	if a := argv(); !strings.Contains(a, "-n\n+15551112222,5553334444") {
		t.Errorf("recipients should be trimmed and comma-joined:\n%s", a)
	}
}

func TestSendSMSRejectsBadNumber(t *testing.T) {
	// Validation happens before any exec, so no fake binary is needed.
	for _, bad := range []string{"", "  ", "not-a-number", "555 123 4567", "+1;rm -rf /", "-h", "+1552,,bad!"} {
		if _, isErr := resultText(t, mustCall(t, handleSendSMS, map[string]any{"number": bad, "text": "x"})); !isErr {
			t.Errorf("number %q should be rejected", bad)
		}
	}
}

func TestSendSMSRequiresText(t *testing.T) {
	if _, isErr := resultText(t, mustCall(t, handleSendSMS, map[string]any{"number": "+15551234567"})); !isErr {
		t.Error("missing text should error")
	}
	if _, isErr := resultText(t, mustCall(t, handleSendSMS, map[string]any{"number": "+15551234567", "text": "   "})); !isErr {
		t.Error("blank text should error")
	}
}

func TestSendSMSRejectsOversizeText(t *testing.T) {
	big := strings.Repeat("a", maxBodyRunes+1)
	if _, isErr := resultText(t, mustCall(t, handleSendSMS, map[string]any{"number": "+15551234567", "text": big})); !isErr {
		t.Error("oversize text should be rejected")
	}
}

func TestSendSMSRejectsBadSlot(t *testing.T) {
	if _, isErr := resultText(t, mustCall(t, handleSendSMS, map[string]any{"number": "+15551234567", "text": "x", "sim_slot": 999.0})); !isErr {
		t.Error("out-of-range sim_slot should error")
	}
}

func TestSendSMSNonZeroExit(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binSMSSend, "echo 'permission denied' 1>&2\nexit 1")

	got, isErr := resultText(t, mustCall(t, handleSendSMS, map[string]any{"number": "+15551234567", "text": "x"}))
	if !isErr {
		t.Fatalf("expected error result, got %s", got)
	}
	if !strings.Contains(got, "permission denied") || !strings.Contains(got, `"exit_code":1`) {
		t.Errorf("error should carry stderr and exit code: %s", got)
	}
}

func TestParseRecipients(t *testing.T) {
	joined, nums, err := parseRecipients("+1555, 800900 ,+1555")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if joined != "+1555,800900,+1555" || len(nums) != 3 {
		t.Errorf("got %q %v", joined, nums)
	}
	for _, bad := range []string{"", "  ", "ab", "12", "+", "1 2 3"} {
		if _, _, err := parseRecipients(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestNormalizeNumber(t *testing.T) {
	cases := map[string]string{
		"+1 (555) 123-4567": "+15551234567",
		"555-987-6543":      "5559876543",
		"":                  "",
	}
	for in, want := range cases {
		if got := normalizeNumber(in); got != want {
			t.Errorf("normalizeNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJSONPassthrough(t *testing.T) {
	got, isErr := resultText(t, must(jsonPassthrough(`[{"id":1}]`+"\n")))
	if isErr || got != `[{"id":1}]` {
		t.Errorf("valid JSON should pass through trimmed, got %q err=%v", got, isErr)
	}
	got, isErr = resultText(t, must(jsonPassthrough("not json")))
	if isErr || got != `{"raw":"not json"}` {
		t.Errorf("non-JSON should be wrapped, got %q", got)
	}
}

func must(res *mcp.CallToolResult, err error) *mcp.CallToolResult {
	if err != nil {
		panic(err)
	}
	return res
}
