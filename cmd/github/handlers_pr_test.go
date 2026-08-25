package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHandleGetPRMapsHeadAndBase(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 7, "title": "add llm proxy", "state": "open",
			"html_url":  "https://github.com/octo/hello/pull/7",
			"user":      map[string]any{"login": "kahz12"},
			"draft":     true,
			"merged":    false,
			"mergeable": true,
			"head": map[string]any{
				"ref":  "feature/llmproxy",
				"repo": map[string]any{"full_name": "kahz12/hello"},
			},
			"base": map[string]any{"ref": "main"},
		})
	})

	var got prSummary
	res, err := handleGetPR(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(7),
	}))
	decodeOK(t, res, err, &got)

	// head is reported fork-qualified so a cross-repo PR is unambiguous.
	if got.Head != "kahz12/hello:feature/llmproxy" {
		t.Errorf("head = %q, want owner-qualified ref", got.Head)
	}
	if got.Base != "main" {
		t.Errorf("base = %q, want main", got.Base)
	}
	if !got.Draft || got.Merged {
		t.Errorf("draft/merged = %v/%v, want true/false", got.Draft, got.Merged)
	}
	if got.Mergeable == nil || !*got.Mergeable {
		t.Errorf("mergeable = %v, want a non-nil true", got.Mergeable)
	}
}

func TestHandleGetPRReportsAPIErrors(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/pulls/999", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusNotFound, "Not Found")
	})

	res, err := handleGetPR(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(999),
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "Not Found") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

func TestHandleCreatePRSendsHeadBaseAndDraft(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		sent.Store(requestBody(t, r))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 12, "title": "add llm proxy", "state": "open", "draft": true,
			"base": map[string]any{"ref": "main"},
		})
	})

	var got prSummary
	res, err := handleCreatePR(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello",
		"title": "add llm proxy", "head": "feature/llmproxy", "base": "main",
		"body": "cierra #42", "draft": true,
	}))
	decodeOK(t, res, err, &got)

	body, _ := sent.Load().(map[string]any)
	if body == nil {
		t.Fatal("the handler never issued the POST")
	}
	// Getting head and base backwards would open the PR in the wrong direction.
	if body["head"] != "feature/llmproxy" {
		t.Errorf("head sent = %v, want feature/llmproxy", body["head"])
	}
	if body["base"] != "main" {
		t.Errorf("base sent = %v, want main", body["base"])
	}
	if body["title"] != "add llm proxy" || body["body"] != "cierra #42" {
		t.Errorf("title/body sent = %v / %v", body["title"], body["body"])
	}
	if body["draft"] != true {
		t.Errorf("draft sent = %v, want true", body["draft"])
	}
	if got.Number != 12 {
		t.Errorf("number = %d, want 12", got.Number)
	}
}

func TestHandleCreatePRDefaultsToNotDraft(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/pulls", func(w http.ResponseWriter, r *http.Request) {
		sent.Store(requestBody(t, r))
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 13})
	})

	res, err := handleCreatePR(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "title": "t", "head": "h", "base": "main",
	}))
	decodeOK(t, res, err, nil)

	body, _ := sent.Load().(map[string]any)
	if draft, ok := body["draft"]; ok && draft != false {
		t.Errorf("draft sent = %v, want false by default", draft)
	}
}

func TestHandleCreatePRReportsAPIErrors(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/pulls", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusUnprocessableEntity, "No commits between main and feature")
	})

	res, err := handleCreatePR(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "title": "t", "head": "feature", "base": "main",
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "No commits between") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

func TestHandleReviewPRSubmitsTheEvent(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		sent.Store(requestBody(t, r))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 555, "state": "APPROVED", "body": "buena pinta",
			"user": map[string]any{"login": "kahz12"},
		})
	})

	var got reviewSummary
	res, err := handleReviewPR(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(7),
		"event": "approve", "body": "buena pinta",
	}))
	decodeOK(t, res, err, &got)

	body, _ := sent.Load().(map[string]any)
	// The handler upper-cases the event; GitHub rejects a lowercase one.
	if body["event"] != "APPROVE" {
		t.Errorf("event sent = %v, want APPROVE", body["event"])
	}
	if body["body"] != "buena pinta" {
		t.Errorf("body sent = %v", body["body"])
	}
	if got.ID != 555 || got.State != "APPROVED" {
		t.Errorf("unexpected review summary: %+v", got)
	}
}

func TestHandleReviewPROmitsAnEmptyBody(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		sent.Store(requestBody(t, r))
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "state": "APPROVED"})
	})

	res, err := handleReviewPR(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(7), "event": "APPROVE",
	}))
	decodeOK(t, res, err, nil)

	body, _ := sent.Load().(map[string]any)
	if _, present := body["body"]; present {
		t.Errorf("body = %v, want the key omitted when empty", body["body"])
	}
}

func TestHandleReviewPRRequiresABodyToRequestChanges(t *testing.T) {
	res, err := handleReviewPR(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(7), "event": "REQUEST_CHANGES",
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "body is required") {
		t.Errorf("error = %q, want it to demand a body", text)
	}
}

func TestHandleMergePRSendsMethodAndTitle(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/pulls/7/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		sent.Store(requestBody(t, r))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha": "mergedsha", "merged": true, "message": "Pull Request successfully merged",
		})
	})

	var got mergeSummary
	res, err := handleMergePR(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(7),
		"merge_method": "SQUASH", "commit_title": "feat: llm proxy",
		"commit_message": "detalles", "sha": "headsha",
	}))
	decodeOK(t, res, err, &got)

	body, _ := sent.Load().(map[string]any)
	// The handler lower-cases the method; GitHub only accepts lowercase.
	if body["merge_method"] != "squash" {
		t.Errorf("merge_method sent = %v, want squash", body["merge_method"])
	}
	if body["commit_title"] != "feat: llm proxy" || body["commit_message"] != "detalles" {
		t.Errorf("commit title/message sent = %v / %v", body["commit_title"], body["commit_message"])
	}
	// sha guards against merging a head that moved since the caller looked.
	if body["sha"] != "headsha" {
		t.Errorf("sha sent = %v, want headsha", body["sha"])
	}
	if !got.Merged || got.SHA != "mergedsha" {
		t.Errorf("unexpected merge summary: %+v", got)
	}
}

func TestHandleMergePRReportsARefusedMerge(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/pulls/7/merge", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusMethodNotAllowed, "Pull Request is not mergeable")
	})

	res, err := handleMergePR(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(7),
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "not mergeable") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

func TestHandlePRToolsRequireTheirArguments(t *testing.T) {
	cases := []struct {
		name    string
		handler handlerFn
		args    map[string]any
		missing string
	}{
		{"get_pr without number", handleGetPR, map[string]any{"owner": "o", "repo": "r"}, "number"},
		{"create_pr without head", handleCreatePR,
			map[string]any{"owner": "o", "repo": "r", "title": "t", "base": "main"}, "head"},
		{"create_pr without base", handleCreatePR,
			map[string]any{"owner": "o", "repo": "r", "title": "t", "head": "f"}, "base"},
		{"create_pr without title", handleCreatePR,
			map[string]any{"owner": "o", "repo": "r", "head": "f", "base": "main"}, "title"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.handler(context.Background(), callRequest(tc.args))
			text := mustErr(t, res, err)
			if !strings.Contains(text, tc.missing) {
				t.Errorf("error = %q, want it to name the missing %q argument", text, tc.missing)
			}
		})
	}
}

func TestPRSummaryFromNil(t *testing.T) {
	got := prSummaryFrom(nil)
	if got.Number != 0 || got.Title != "" || got.Head != "" || got.Mergeable != nil {
		t.Errorf("prSummaryFrom(nil) = %+v, want the zero value", got)
	}
}
