package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHandleCreateIssueSendsTitleBodyAndLabels(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		sent.Store(requestBody(t, r))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 42, "title": "se rompe al arrancar", "state": "open",
			"html_url": "https://github.com/octo/hello/issues/42",
			"user":     map[string]any{"login": "kahz12"},
			"labels":   []map[string]any{{"name": "bug"}, {"name": "p1"}},
			"comments": 0,
		})
	})

	var got issueSummary
	res, err := handleCreateIssue(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello",
		"title": "se rompe al arrancar", "body": "pasos para reproducir",
		"labels": []any{"bug", "p1"},
	}))
	decodeOK(t, res, err, &got)

	body, _ := sent.Load().(map[string]any)
	if body == nil {
		t.Fatal("the handler never issued the POST")
	}
	if body["title"] != "se rompe al arrancar" {
		t.Errorf("title sent = %v", body["title"])
	}
	if body["body"] != "pasos para reproducir" {
		t.Errorf("body sent = %v", body["body"])
	}
	labels, _ := body["labels"].([]any)
	if len(labels) != 2 || labels[0] != "bug" || labels[1] != "p1" {
		t.Errorf("labels sent = %v, want [bug p1]", body["labels"])
	}
	if got.Number != 42 || got.State != "open" || got.User != "kahz12" {
		t.Errorf("unexpected summary: %+v", got)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "bug" {
		t.Errorf("labels = %v, want the names flattened", got.Labels)
	}
}

func TestHandleCreateIssueOmitsLabelsWhenNoneGiven(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/issues", func(w http.ResponseWriter, r *http.Request) {
		sent.Store(requestBody(t, r))
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 1, "state": "open"})
	})

	res, err := handleCreateIssue(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "title": "sin etiquetas",
	}))
	decodeOK(t, res, err, nil)

	body, _ := sent.Load().(map[string]any)
	if _, present := body["labels"]; present {
		t.Errorf("labels = %v, want the key absent when the caller sent none", body["labels"])
	}
}

func TestHandleCreateIssueReportsAPIErrors(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/issues", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusForbidden, "Issues are disabled for this repo")
	})

	res, err := handleCreateIssue(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "title": "t",
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "Issues are disabled") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

func TestHandleListIssuesDefaultsToOpen(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var query atomic.Value
	mux.HandleFunc("/repos/octo/hello/issues", func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.RawQuery)
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4998")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"number": 7, "title": "uno", "state": "open"},
			{"number": 9, "title": "dos", "state": "open"},
		})
	})

	var got listResponse[issueSummary]
	res, err := handleListIssues(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello",
	}))
	decodeOK(t, res, err, &got)

	q, _ := query.Load().(string)
	if !strings.Contains(q, "state=open") {
		t.Errorf("query = %q, want the default state=open", q)
	}
	if got.Count != 2 || got.Items[1].Number != 9 {
		t.Errorf("unexpected list: %+v", got)
	}
	if got.RateLimit == nil || got.RateLimit.Remaining != 4998 {
		t.Errorf("rate limit = %+v, want it read off the response headers", got.RateLimit)
	}
}

func TestHandleListIssuesForwardsStateAndPaging(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var query atomic.Value
	mux.HandleFunc("/repos/octo/hello/issues", func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})

	var got listResponse[issueSummary]
	res, err := handleListIssues(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "state": "closed",
		"per_page": float64(50), "page": float64(2),
	}))
	decodeOK(t, res, err, &got)

	q, _ := query.Load().(string)
	for _, want := range []string{"state=closed", "per_page=50", "page=2"} {
		if !strings.Contains(q, want) {
			t.Errorf("query = %q, missing %q", q, want)
		}
	}
	if got.Items == nil {
		t.Error("items = null, want an empty array")
	}
}

func TestHandleCommentIssuePostsTheBody(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		sent.Store(requestBody(t, r))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 1234, "body": "lo miro mañana",
			"user":     map[string]any{"login": "kahz12"},
			"html_url": "https://github.com/octo/hello/issues/42#issuecomment-1234",
		})
	})

	var got commentSummary
	res, err := handleCommentIssue(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(42), "body": "lo miro mañana",
	}))
	decodeOK(t, res, err, &got)

	body, _ := sent.Load().(map[string]any)
	if body["body"] != "lo miro mañana" {
		t.Errorf("body sent = %v", body["body"])
	}
	if got.ID != 1234 || got.User != "kahz12" {
		t.Errorf("unexpected comment summary: %+v", got)
	}
}

func TestHandleCloseIssueSendsClosedState(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/issues/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}
		sent.Store(requestBody(t, r))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 42, "state": "closed", "title": "resuelto",
		})
	})

	var got issueSummary
	res, err := handleCloseIssue(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(42),
	}))
	decodeOK(t, res, err, &got)

	body, _ := sent.Load().(map[string]any)
	if body["state"] != "closed" {
		t.Errorf("state sent = %v, want closed", body["state"])
	}
	if _, present := body["state_reason"]; present {
		t.Errorf("state_reason = %v, want it omitted when the caller gave none", body["state_reason"])
	}
	if got.State != "closed" {
		t.Errorf("state = %q, want closed", got.State)
	}
}

func TestHandleCloseIssueForwardsStateReason(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/issues/42", func(w http.ResponseWriter, r *http.Request) {
		sent.Store(requestBody(t, r))
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "state": "closed"})
	})

	res, err := handleCloseIssue(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(42), "state_reason": "not_planned",
	}))
	decodeOK(t, res, err, nil)

	body, _ := sent.Load().(map[string]any)
	if body["state_reason"] != "not_planned" {
		t.Errorf("state_reason sent = %v, want not_planned", body["state_reason"])
	}
}

// Append and replace hit different verbs. Sending the replace verb for an
// append would wipe the labels already on the issue.
func TestHandleLabelIssueAppendsWithPOST(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var method atomic.Value
	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/issues/42/labels", func(w http.ResponseWriter, r *http.Request) {
		method.Store(r.Method)
		var labels []string
		_ = json.NewDecoder(r.Body).Decode(&labels)
		sent.Store(labels)
		_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "bug"}, {"name": "p1"}})
	})

	res, err := handleLabelIssue(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(42),
		"labels": []any{"bug", "p1"},
	}))
	var got struct {
		Labels []string `json:"labels"`
		Count  int      `json:"count"`
	}
	decodeOK(t, res, err, &got)

	if m, _ := method.Load().(string); m != http.MethodPost {
		t.Errorf("method = %s, want POST for an append", m)
	}
	if labels, _ := sent.Load().([]string); len(labels) != 2 || labels[0] != "bug" {
		t.Errorf("labels sent = %v, want [bug p1]", labels)
	}
	if got.Count != 2 || got.Labels[1] != "p1" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestHandleLabelIssueReplacesWithPUT(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var method atomic.Value
	mux.HandleFunc("/repos/octo/hello/issues/42/labels", func(w http.ResponseWriter, r *http.Request) {
		method.Store(r.Method)
		_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "solo-esta"}})
	})

	res, err := handleLabelIssue(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(42),
		"labels": []any{"solo-esta"}, "replace": true,
	}))
	decodeOK(t, res, err, nil)

	if m, _ := method.Load().(string); m != http.MethodPut {
		t.Errorf("method = %s, want PUT for a replace", m)
	}
}

func TestHandleLabelIssueReportsAPIErrors(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/issues/42/labels", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusNotFound, "Not Found")
	})

	res, err := handleLabelIssue(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "number": float64(42), "labels": []any{"bug"},
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "Not Found") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

func TestHandleIssueToolsRequireTheirArguments(t *testing.T) {
	cases := []struct {
		name    string
		handler handlerFn
		args    map[string]any
		missing string
	}{
		{"create_issue without title", handleCreateIssue, map[string]any{"owner": "o", "repo": "r"}, "title"},
		{"create_issue without repo", handleCreateIssue, map[string]any{"owner": "o", "title": "t"}, "repo"},
		{"list_issues without owner", handleListIssues, map[string]any{"repo": "r"}, "owner"},
		{"comment_issue without body", handleCommentIssue,
			map[string]any{"owner": "o", "repo": "r", "number": float64(1)}, "body"},
		{"comment_issue without number", handleCommentIssue,
			map[string]any{"owner": "o", "repo": "r", "body": "b"}, "number"},
		{"close_issue without number", handleCloseIssue, map[string]any{"owner": "o", "repo": "r"}, "number"},
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

func TestIssueSummaryFromNil(t *testing.T) {
	got := issueSummaryFrom(nil)
	if got.Number != 0 || got.Title != "" || got.State != "" || got.Labels != nil {
		t.Errorf("issueSummaryFrom(nil) = %+v, want the zero value", got)
	}
}
