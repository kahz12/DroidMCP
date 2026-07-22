package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// sampleContacts is a small address book the fake termux-contact-list emits.
const sampleContacts = `[
	{"name":"Alice Smith","number":"+1 (555) 123-4567"},
	{"name":"Bob Jones","number":"555-987-6543"},
	{"name":"Carla Díaz","number":"+34 600 11 22 33"},
	{"name":"Alice Cooper","number":""}
]`

func fakeContactList(t *testing.T, body string) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binContactList, "cat <<'EOF'\n"+body+"\nEOF")
}

func TestSearchContactsByName(t *testing.T) {
	fakeContactList(t, sampleContacts)

	got, isErr := resultText(t, mustCall(t, handleSearchContacts, map[string]any{"query": "alice"}))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out searchResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Count != 2 {
		t.Fatalf("expected 2 Alice matches, got %d (%s)", out.Count, got)
	}
	for _, c := range out.Contacts {
		if !strings.HasPrefix(c.Name, "Alice") {
			t.Errorf("unexpected match: %+v", c)
		}
	}
}

func TestSearchContactsByNumberIgnoresFormatting(t *testing.T) {
	fakeContactList(t, sampleContacts)

	// Query with different formatting than stored must still match.
	got, _ := resultText(t, mustCall(t, handleSearchContacts, map[string]any{"query": "5551234567"}))
	var out searchResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Count != 1 || out.Contacts[0].Name != "Alice Smith" {
		t.Errorf("number match failed: %s", got)
	}
}

func TestSearchContactsQueryRequired(t *testing.T) {
	// Rejected before any exec, so no fake binary is needed.
	if _, isErr := resultText(t, mustCall(t, handleSearchContacts, nil)); !isErr {
		t.Error("missing query should error")
	}
	if _, isErr := resultText(t, mustCall(t, handleSearchContacts, map[string]any{"query": "  "})); !isErr {
		t.Error("blank query should error")
	}
}

func TestSearchContactsLimit(t *testing.T) {
	fakeContactList(t, sampleContacts)

	got, _ := resultText(t, mustCall(t, handleSearchContacts, map[string]any{"query": "a", "limit": 1.0}))
	var out searchResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Count != 1 {
		t.Errorf("limit not applied: got %d", out.Count)
	}
}

// TestSearchContactsQueryNeverReachesArgv locks in the core safety property:
// the caller-supplied query is filtered in memory and never passed to the
// backend command line.
func TestSearchContactsQueryNeverReachesArgv(t *testing.T) {
	dir := fakeBinDir(t)
	readArgs := captureArgs(t, dir)
	fakeBin(t, dir, binContactList, recordArgv)

	if _, isErr := resultText(t, mustCall(t, handleSearchContacts, map[string]any{"query": "; rm -rf /"})); isErr {
		t.Fatal("search should succeed against an empty list")
	}
	if argv := strings.TrimSpace(readArgs()); argv != "" {
		t.Errorf("termux-contact-list must receive no arguments, got %q", argv)
	}
}

func TestGetContactByName(t *testing.T) {
	fakeContactList(t, sampleContacts)

	got, _ := resultText(t, mustCall(t, handleGetContact, map[string]any{"name": "bob jones"}))
	var out getResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if !out.Found || out.Count != 1 || out.Contacts[0].Number != "555-987-6543" {
		t.Errorf("exact name match failed: %s", got)
	}
}

func TestGetContactByNumber(t *testing.T) {
	fakeContactList(t, sampleContacts)

	got, _ := resultText(t, mustCall(t, handleGetContact, map[string]any{"number": "+34 600112233"}))
	var out getResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if !out.Found || out.Count != 1 || out.Contacts[0].Name != "Carla Díaz" {
		t.Errorf("number match failed: %s", got)
	}
}

func TestGetContactNotFound(t *testing.T) {
	fakeContactList(t, sampleContacts)

	got, isErr := resultText(t, mustCall(t, handleGetContact, map[string]any{"name": "nobody"}))
	if isErr {
		t.Fatalf("not-found is a normal result, not an error: %s", got)
	}
	var out getResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Found || out.Count != 0 || out.Contacts == nil {
		t.Errorf("expected found=false with empty (non-null) array: %s", got)
	}
}

func TestGetContactRequiresArg(t *testing.T) {
	if _, isErr := resultText(t, mustCall(t, handleGetContact, nil)); !isErr {
		t.Error("missing name and number should error")
	}
}

func TestListGroupsStub(t *testing.T) {
	got, isErr := resultText(t, mustCall(t, handleListGroups, nil))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out groupsResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Supported || out.Groups == nil || out.Note == "" {
		t.Errorf("stub should report supported=false with an explanatory note: %s", got)
	}
}

func TestExportContactsJSON(t *testing.T) {
	fakeContactList(t, sampleContacts)

	got, _ := resultText(t, mustCall(t, handleExportContacts, map[string]any{"format": "json"}))
	var out exportJSONResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Format != "json" || out.Count != 4 {
		t.Errorf("expected all 4 contacts as json: %s", got)
	}
}

func TestExportContactsVCard(t *testing.T) {
	fakeContactList(t, sampleContacts)

	got, _ := resultText(t, mustCall(t, handleExportContacts, map[string]any{"format": "vcard", "query": "carla"}))
	var out exportVCardResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Format != "vcard" || out.Count != 1 {
		t.Fatalf("expected one filtered vcard: %s", got)
	}
	for _, want := range []string{"BEGIN:VCARD", "VERSION:3.0", "FN:Carla Díaz", "TEL:+34 600 11 22 33", "END:VCARD"} {
		if !strings.Contains(out.VCard, want) {
			t.Errorf("vcard missing %q:\n%s", want, out.VCard)
		}
	}
}

func TestExportContactsUnknownFormat(t *testing.T) {
	// Format is validated before any exec.
	if _, isErr := resultText(t, mustCall(t, handleExportContacts, map[string]any{"format": "xml"})); !isErr {
		t.Error("unknown format should error")
	}
}

func TestExportContactsOmitsEmptyTEL(t *testing.T) {
	fakeContactList(t, sampleContacts)

	// "Alice Cooper" has an empty number and must not emit a bare TEL line.
	got, _ := resultText(t, mustCall(t, handleExportContacts, map[string]any{"format": "vcard", "query": "cooper"}))
	var out exportVCardResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if strings.Contains(out.VCard, "TEL:") {
		t.Errorf("empty number should not produce a TEL line:\n%s", out.VCard)
	}
	if !strings.Contains(out.VCard, "FN:Alice Cooper") {
		t.Errorf("expected the contact's FN line:\n%s", out.VCard)
	}
}

func TestVCardEscape(t *testing.T) {
	got := vcardEscape("Doe; John, \"CEO\"\nline2")
	for _, want := range []string{`\;`, `\,`, `\n`} {
		if !strings.Contains(got, want) {
			t.Errorf("escape missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "\n") {
		t.Errorf("raw newline should be escaped: %q", got)
	}
}

func TestParseContactsEmpty(t *testing.T) {
	c, err := parseContacts("  \n")
	if err != nil || c == nil || len(c) != 0 {
		t.Errorf("empty output should parse to empty slice: %v %v", c, err)
	}
}

func TestParseContactsInvalid(t *testing.T) {
	if _, err := parseContacts("not json"); err == nil {
		t.Error("invalid JSON should error")
	}
}

func TestNormalizeNumber(t *testing.T) {
	cases := map[string]string{
		"+1 (555) 123-4567": "+15551234567",
		"555-987-6543":      "5559876543",
		"  555.123.4567 ":   "5551234567",
		"":                  "",
	}
	for in, want := range cases {
		if got := normalizeNumber(in); got != want {
			t.Errorf("normalizeNumber(%q) = %q, want %q", in, got, want)
		}
	}
}
