package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
}

func resultText(t *testing.T, res *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected at least one content block")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text, res.IsError
}

// fakeClipboardEnv installs shell shims for termux-clipboard-{get,set} into
// a temp dir, prepends it to PATH, and resets the in-process history. The
// store path is baked into the shim scripts so tests do not need to share
// state through env vars.
func fakeClipboardEnv(t *testing.T) (storePath string) {
	t.Helper()
	requireSh(t)
	dir := t.TempDir()
	store := filepath.Join(dir, "clip.bin")
	if err := os.WriteFile(store, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	setShim := "#!/bin/sh\ncat > " + store + "\n"
	getShim := "#!/bin/sh\ncat " + store + " 2>/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "termux-clipboard-set"), []byte(setShim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "termux-clipboard-get"), []byte(getShim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	history = newHistoryFromEnv()
	return store
}

func TestHandleSetThenGetRoundtrip(t *testing.T) {
	fakeClipboardEnv(t)
	res, err := handleSetClipboard(context.Background(), callRequest(map[string]any{
		"text": "hello clipboard",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("set failed: %s", text)
	}
	var setOut setClipboardResult
	if jerr := json.Unmarshal([]byte(text), &setOut); jerr != nil {
		t.Fatalf("set not JSON: %v\n%s", jerr, text)
	}
	if !setOut.OK || setOut.Source != "text" || setOut.BytesLen != len("hello clipboard") {
		t.Errorf("unexpected set result: %+v", setOut)
	}

	res, err = handleGetClipboard(context.Background(), callRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr = resultText(t, res)
	if isErr {
		t.Fatalf("get failed: %s", text)
	}
	var getOut getClipboardResult
	if jerr := json.Unmarshal([]byte(text), &getOut); jerr != nil {
		t.Fatalf("get not JSON: %v\n%s", jerr, text)
	}
	if getOut.Text != "hello clipboard" {
		t.Errorf("text: %q", getOut.Text)
	}
	if !getOut.IsUTF8 {
		t.Errorf("expected is_utf8=true, got %+v", getOut)
	}
	if getOut.BytesLen != len("hello clipboard") {
		t.Errorf("bytes_len: %d", getOut.BytesLen)
	}
	decoded, _ := base64.StdEncoding.DecodeString(getOut.Base64)
	if string(decoded) != "hello clipboard" {
		t.Errorf("base64 round-trip mismatch: %q", decoded)
	}
}

func TestHandleSetClipboardBase64Roundtrip(t *testing.T) {
	fakeClipboardEnv(t)
	// Non-UTF-8 payload (a couple of high bytes).
	binary := []byte{0xff, 0x00, 0xfe, 0x42}
	encoded := base64.StdEncoding.EncodeToString(binary)
	res, err := handleSetClipboard(context.Background(), callRequest(map[string]any{
		"text_base64": encoded,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("set failed: %s", text)
	}

	res, err = handleGetClipboard(context.Background(), callRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr = resultText(t, res)
	if isErr {
		t.Fatalf("get failed: %s", text)
	}
	var getOut getClipboardResult
	if jerr := json.Unmarshal([]byte(text), &getOut); jerr != nil {
		t.Fatalf("get not JSON: %v", jerr)
	}
	if getOut.IsUTF8 {
		t.Errorf("expected is_utf8=false for binary payload: %+v", getOut)
	}
	decoded, derr := base64.StdEncoding.DecodeString(getOut.Base64)
	if derr != nil {
		t.Fatalf("base64 decode: %v", derr)
	}
	if string(decoded) != string(binary) {
		t.Errorf("binary round-trip mismatch: got %x, want %x", decoded, binary)
	}
}

func TestHandleSetClipboardRejectsBoth(t *testing.T) {
	fakeClipboardEnv(t)
	res, err := handleSetClipboard(context.Background(), callRequest(map[string]any{
		"text":        "a",
		"text_base64": base64.StdEncoding.EncodeToString([]byte("b")),
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected error when both text and text_base64 supplied")
	}
	if !strings.Contains(text, "exactly one") {
		t.Errorf("expected the mutual-exclusion error, got %q", text)
	}
}

func TestHandleSetClipboardRejectsNeither(t *testing.T) {
	fakeClipboardEnv(t)
	res, err := handleSetClipboard(context.Background(), callRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected error when neither text nor text_base64 supplied")
	}
	if !strings.Contains(text, "exactly one") {
		t.Errorf("expected the mutual-exclusion error, got %q", text)
	}
}

func TestHandleSetClipboardInvalidBase64(t *testing.T) {
	fakeClipboardEnv(t)
	res, err := handleSetClipboard(context.Background(), callRequest(map[string]any{
		"text_base64": "!!!not-base64!!!",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatalf("expected base64 decode error, got: %s", text)
	}
	if !strings.Contains(text, "text_base64") {
		t.Errorf("expected error to mention text_base64, got %q", text)
	}
}

func TestHandleClearClipboardClearsHistory(t *testing.T) {
	fakeClipboardEnv(t)
	_, err := handleSetClipboard(context.Background(), callRequest(map[string]any{
		"text": "to be cleared",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(history.Snapshot()); got != 1 {
		t.Fatalf("expected 1 history entry before clear, got %d", got)
	}

	res, err := handleClearClipboard(context.Background(), callRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("clear failed: %s", text)
	}
	var got clearClipboardResult
	if jerr := json.Unmarshal([]byte(text), &got); jerr != nil {
		t.Fatalf("clear not JSON: %v", jerr)
	}
	if !got.OK || !got.HistoryCleared {
		t.Errorf("unexpected clear result: %+v", got)
	}
	if snap := history.Snapshot(); len(snap) != 0 {
		t.Errorf("expected history to be empty after clear, got %+v", snap)
	}
}

func TestHandleClipboardHistoryShape(t *testing.T) {
	fakeClipboardEnv(t)
	if _, err := handleSetClipboard(context.Background(), callRequest(map[string]any{
		"text": "first",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := handleSetClipboard(context.Background(), callRequest(map[string]any{
		"text_base64": base64.StdEncoding.EncodeToString([]byte{0xff, 0x42}),
	})); err != nil {
		t.Fatal(err)
	}

	res, err := handleClipboardHistory(context.Background(), callRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("history failed: %s", text)
	}
	var got historyResult
	if jerr := json.Unmarshal([]byte(text), &got); jerr != nil {
		t.Fatalf("history not JSON: %v\n%s", jerr, text)
	}
	if got.Count != 2 || len(got.Entries) != 2 {
		t.Fatalf("expected 2 entries, got count=%d entries=%d", got.Count, len(got.Entries))
	}
	if got.Entries[0].Source != "text" || got.Entries[0].Text != "first" {
		t.Errorf("unexpected first entry: %+v", got.Entries[0])
	}
	if got.Entries[1].Source != "base64" || got.Entries[1].IsUTF8 {
		t.Errorf("unexpected second entry: %+v", got.Entries[1])
	}
	if got.MaxEntries <= 0 || got.MaxBytes <= 0 {
		t.Errorf("expected positive caps, got %+v", got)
	}
}

func TestHandleGetClipboardMissingBinary(t *testing.T) {
	prev := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = prev })

	res, err := handleGetClipboard(context.Background(), callRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected missing-binary error")
	}
	if !strings.Contains(text, "termux-api") {
		t.Errorf("expected hint about termux-api, got %q", text)
	}
}

func TestHandleSetClipboardEmptyText(t *testing.T) {
	// Setting "text" to "" is allowed (writes empty content).
	fakeClipboardEnv(t)
	res, err := handleSetClipboard(context.Background(), callRequest(map[string]any{
		"text": "",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("expected empty-text set to succeed, got: %s", text)
	}
	var got setClipboardResult
	if jerr := json.Unmarshal([]byte(text), &got); jerr != nil {
		t.Fatalf("not JSON: %v", jerr)
	}
	if got.BytesLen != 0 || got.Source != "text" {
		t.Errorf("unexpected: %+v", got)
	}
}

// Android 10+ hands a background reader an empty clipboard with no error, so
// an empty read must say it may not be empty; a non-empty one needs no note.
func TestHandleGetClipboardNotesThatEmptyMayBeBlocked(t *testing.T) {
	store := fakeClipboardEnv(t)
	read := func() getClipboardResult {
		t.Helper()
		res, err := handleGetClipboard(context.Background(), callRequest(nil))
		if err != nil {
			t.Fatal(err)
		}
		text, isErr := resultText(t, res)
		if isErr {
			t.Fatalf("get failed: %s", text)
		}
		var out getClipboardResult
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, text)
		}
		return out
	}

	if out := read(); out.BytesLen != 0 || !strings.Contains(out.Note, "does not prove the clipboard is empty") {
		t.Errorf("empty read without the note: %+v", out)
	}
	if err := os.WriteFile(store, []byte("hola"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := read(); out.Text != "hola" || out.Note != "" {
		t.Errorf("non-empty read: %+v, want the text and no note", out)
	}
}
