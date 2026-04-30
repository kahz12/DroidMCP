package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v60/github"
	"github.com/mark3labs/mcp-go/mcp"
)

// newTestClient stands up an httptest server, points a real *github.Client at
// it, and installs the client as the package-level ghClient. The returned
// cleanup function tears it all down. The caller registers handlers per route
// on the mux.
func newTestClient(t *testing.T) (*http.ServeMux, func()) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)

	c := github.NewClient(nil)
	u, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	c.BaseURL = u
	c.UploadURL = u

	prev := ghClient
	ghClient = c

	return mux, func() {
		srv.Close()
		ghClient = prev
	}
}

func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	}
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

func TestResolveGitHubToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_APP_TOKEN", "")
	t.Setenv("GITHUB_FINE_GRAINED_TOKEN", "")

	if tok, src := resolveGitHubToken(); tok != "" || src != "" {
		t.Fatalf("expected empty result, got %q from %q", tok, src)
	}

	t.Setenv("GITHUB_FINE_GRAINED_TOKEN", "fine")
	if tok, src := resolveGitHubToken(); tok != "fine" || src != "GITHUB_FINE_GRAINED_TOKEN" {
		t.Fatalf("fine-grained fallback failed: %q from %q", tok, src)
	}

	// GITHUB_TOKEN should win when present alongside the others.
	t.Setenv("GITHUB_TOKEN", "primary")
	t.Setenv("GITHUB_APP_TOKEN", "app")
	if tok, src := resolveGitHubToken(); tok != "primary" || src != "GITHUB_TOKEN" {
		t.Fatalf("priority order broken: %q from %q", tok, src)
	}
}

func TestPaginationOpts(t *testing.T) {
	opts := paginationOpts(callRequest(map[string]any{"page": float64(3), "per_page": float64(50)}))
	if opts.Page != 3 || opts.PerPage != 50 {
		t.Fatalf("unexpected pagination: %+v", opts)
	}
	zero := paginationOpts(callRequest(map[string]any{}))
	if zero.Page != 0 || zero.PerPage != 0 {
		t.Fatalf("expected zero defaults, got %+v", zero)
	}
}

func TestHandleGetRepoJSON(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		json.NewEncoder(w).Encode(map[string]any{
			"name":           "hello",
			"full_name":      "octo/hello",
			"description":    "demo",
			"html_url":       "https://github.com/octo/hello",
			"default_branch": "main",
			"private":        false,
			"fork":           false,
			"stargazers_count": 7,
			"forks_count":      2,
			"open_issues_count": 1,
			"language":         "Go",
		})
	})

	res, err := handleGetRepo(context.Background(), callRequest(map[string]any{"owner": "octo", "repo": "hello"}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error result: %s", text)
	}

	var got repoSummary
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, text)
	}
	if got.FullName != "octo/hello" || got.Stars != 7 || got.DefaultBranch != "main" {
		t.Fatalf("unexpected summary: %+v", got)
	}
}

func TestHandleListBranchesProtectedOnly(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var capturedQuery atomic.Value
	mux.HandleFunc("/repos/octo/hello/branches", func(w http.ResponseWriter, r *http.Request) {
		capturedQuery.Store(r.URL.RawQuery)
		json.NewEncoder(w).Encode([]map[string]any{
			{"name": "main", "commit": map[string]any{"sha": "deadbeef"}, "protected": true},
		})
	})

	res, err := handleListBranches(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "protected_only": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error result: %s", text)
	}
	q, _ := capturedQuery.Load().(string)
	if !strings.Contains(q, "protected=true") {
		t.Fatalf("expected protected=true in query, got %q", q)
	}

	var got listResponse[branchSummary]
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if got.Count != 1 || got.Items[0].SHA != "deadbeef" {
		t.Fatalf("unexpected list response: %+v", got)
	}
}

func TestHandleSearchIssues(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/search/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "is:open repo:foo/bar" {
			t.Errorf("unexpected query: %q", got)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"total_count":        2,
			"incomplete_results": false,
			"items": []map[string]any{
				{"number": 1, "title": "first", "state": "open", "html_url": "u1", "comments": 0},
				{"number": 2, "title": "second", "state": "open", "html_url": "u2", "comments": 3},
			},
		})
	})

	res, err := handleSearchIssues(context.Background(), callRequest(map[string]any{
		"query": "is:open repo:foo/bar",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error result: %s", text)
	}
	var got searchResponse[issueSummary]
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if got.Total != 2 || got.Count != 2 || got.Items[1].Number != 2 {
		t.Fatalf("unexpected search response: %+v", got)
	}
}

func TestHandleReviewPRValidation(t *testing.T) {
	res, _ := handleReviewPR(context.Background(), callRequest(map[string]any{
		"owner": "o", "repo": "r", "number": float64(1), "event": "BANANA",
	}))
	text, isErr := resultText(t, res)
	if !isErr || !strings.Contains(text, "APPROVE") {
		t.Fatalf("expected event-validation error, got isErr=%v %q", isErr, text)
	}

	res, _ = handleReviewPR(context.Background(), callRequest(map[string]any{
		"owner": "o", "repo": "r", "number": float64(1), "event": "REQUEST_CHANGES",
	}))
	text, isErr = resultText(t, res)
	if !isErr || !strings.Contains(text, "body is required") {
		t.Fatalf("expected body-required error, got isErr=%v %q", isErr, text)
	}
}

func TestHandleMergePRValidation(t *testing.T) {
	res, _ := handleMergePR(context.Background(), callRequest(map[string]any{
		"owner": "o", "repo": "r", "number": float64(1), "merge_method": "octopus",
	}))
	text, isErr := resultText(t, res)
	if !isErr || !strings.Contains(text, "merge_method") {
		t.Fatalf("expected merge_method error, got isErr=%v %q", isErr, text)
	}
}

func TestHandleLabelIssueRequiresLabels(t *testing.T) {
	res, _ := handleLabelIssue(context.Background(), callRequest(map[string]any{
		"owner": "o", "repo": "r", "number": float64(1), "labels": []any{},
	}))
	text, isErr := resultText(t, res)
	if !isErr || !strings.Contains(text, "labels") {
		t.Fatalf("expected labels error, got isErr=%v %q", isErr, text)
	}
}

func TestStringArrayArg(t *testing.T) {
	got := stringArrayArg(callRequest(map[string]any{
		"labels": []any{"bug", 7, "wontfix"},
	}), "labels")
	if len(got) != 2 || got[0] != "bug" || got[1] != "wontfix" {
		t.Fatalf("unexpected labels: %v", got)
	}
	if r := stringArrayArg(callRequest(map[string]any{}), "labels"); r != nil {
		t.Fatalf("expected nil for missing arg, got %v", r)
	}
}
