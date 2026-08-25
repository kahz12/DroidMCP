package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHandleListReposPaginates(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var query atomic.Value
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.RawQuery)
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4990")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "droidmcp", "full_name": "kahz12/droidmcp", "stargazers_count": 3, "language": "Go"},
			{"name": "vanguard", "full_name": "kahz12/vanguard", "private": true},
		})
	})

	var got listResponse[repoSummary]
	res, err := handleListRepos(context.Background(), callRequest(map[string]any{
		"per_page": float64(20), "page": float64(3),
	}))
	decodeOK(t, res, err, &got)

	q, _ := query.Load().(string)
	for _, want := range []string{"per_page=20", "page=3"} {
		if !strings.Contains(q, want) {
			t.Errorf("query = %q, missing %q", q, want)
		}
	}
	if got.Count != 2 || got.Items[0].FullName != "kahz12/droidmcp" || got.Items[0].Stars != 3 {
		t.Errorf("unexpected list: %+v", got)
	}
	if !got.Items[1].Private {
		t.Error("second repo should be reported as private")
	}
	if got.RateLimit == nil || got.RateLimit.Remaining != 4990 {
		t.Errorf("rate limit = %+v, want it read off the headers", got.RateLimit)
	}
}

func TestHandleListReposReportsAPIErrors(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusUnauthorized, "Bad credentials")
	})

	res, err := handleListRepos(context.Background(), callRequest(nil))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "Bad credentials") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

func TestHandleListTags(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/tags", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "v0.3.0", "commit": map[string]any{"sha": "aaa111"}},
			{"name": "v0.2.0", "commit": map[string]any{"sha": "bbb222"}},
		})
	})

	var got listResponse[tagSummary]
	res, err := handleListTags(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello",
	}))
	decodeOK(t, res, err, &got)

	if got.Count != 2 || got.Items[0].Name != "v0.3.0" || got.Items[0].SHA != "aaa111" {
		t.Errorf("unexpected tags: %+v", got)
	}
}

func TestHandleListReleases(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/releases", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id": 900, "tag_name": "v0.3.0", "name": "Release 0.3.0",
				"draft": false, "prerelease": false, "published_at": "2026-08-01T10:00:00Z",
			},
			{"id": 901, "tag_name": "v0.4.0-rc1", "draft": true, "prerelease": true},
		})
	})

	var got listResponse[releaseSummary]
	res, err := handleListReleases(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello",
	}))
	decodeOK(t, res, err, &got)

	if got.Count != 2 {
		t.Fatalf("count = %d, want 2", got.Count)
	}
	if got.Items[0].ID != 900 || got.Items[0].TagName != "v0.3.0" {
		t.Errorf("unexpected first release: %+v", got.Items[0])
	}
	if got.Items[0].PublishedAt == nil {
		t.Error("published_at = nil for a published release, want a time")
	}
	// An unpublished draft has no date, and it must stay absent rather than
	// serialize as the zero time.
	if got.Items[1].PublishedAt != nil {
		t.Errorf("published_at = %v for a draft, want nil", got.Items[1].PublishedAt)
	}
	if !got.Items[1].Draft || !got.Items[1].Prerelease {
		t.Errorf("draft/prerelease flags lost: %+v", got.Items[1])
	}
}

func TestHandleListCommitsForwardsFilters(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var query atomic.Value
	mux.HandleFunc("/repos/octo/hello/commits", func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"sha":      "c0ffee",
				"html_url": "https://github.com/octo/hello/commit/c0ffee",
				"commit": map[string]any{
					"message":   "fix the thing",
					"author":    map[string]any{"name": "Ale", "date": "2026-08-20T09:00:00Z"},
					"committer": map[string]any{"name": "Ale"},
				},
			},
		})
	})

	var got listResponse[commitSummary]
	res, err := handleListCommits(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello",
		"sha": "main", "path": "cmd/github", "author": "kahz12",
	}))
	decodeOK(t, res, err, &got)

	q, _ := query.Load().(string)
	for _, want := range []string{"sha=main", "author=kahz12", "path=cmd"} {
		if !strings.Contains(q, want) {
			t.Errorf("query = %q, missing %q", q, want)
		}
	}
	if got.Count != 1 {
		t.Fatalf("count = %d, want 1", got.Count)
	}
	c := got.Items[0]
	if c.SHA != "c0ffee" || c.Message != "fix the thing" || c.Author != "Ale" || c.Committer != "Ale" {
		t.Errorf("unexpected commit summary: %+v", c)
	}
	if c.AuthorAt.IsZero() {
		t.Error("author_at is zero, want the commit date")
	}
}

func TestHandleGetCommit(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/commits/c0ffee", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha":    "c0ffee",
			"commit": map[string]any{"message": "fix the thing"},
		})
	})

	var got commitSummary
	res, err := handleGetCommit(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "sha": "c0ffee",
	}))
	decodeOK(t, res, err, &got)

	if got.SHA != "c0ffee" || got.Message != "fix the thing" {
		t.Errorf("unexpected commit: %+v", got)
	}
}

func TestHandleGetCommitReportsAPIErrors(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/commits/nope", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusNotFound, "No commit found for SHA: nope")
	})

	res, err := handleGetCommit(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "sha": "nope",
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "No commit found") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

func TestHandleForkRepoSendsOptions(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var sent atomic.Value
	mux.HandleFunc("/repos/octo/hello/forks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		sent.Store(requestBody(t, r))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "hello", "full_name": "kahz12/hello", "fork": true,
		})
	})

	var got struct {
		repoSummary
		Pending bool `json:"pending"`
	}
	res, err := handleForkRepo(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello",
		"organization": "miorg", "name": "hola", "default_branch_only": true,
	}))
	decodeOK(t, res, err, &got)

	body, _ := sent.Load().(map[string]any)
	if body["organization"] != "miorg" || body["name"] != "hola" || body["default_branch_only"] != true {
		t.Errorf("fork options sent = %v", body)
	}
	if got.FullName != "kahz12/hello" || !got.Fork {
		t.Errorf("unexpected fork summary: %+v", got.repoSummary)
	}
	if got.Pending {
		t.Error("pending = true for a synchronous fork, want false")
	}
}

// GitHub answers 202 when the fork is queued. go-github turns that into an
// AcceptedError, which is a success for us: the payload is still there.
func TestHandleForkRepoReportsAQueuedFork(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/forks", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "hello", "full_name": "kahz12/hello", "fork": true,
		})
	})

	var got struct {
		repoSummary
		Pending bool `json:"pending"`
	}
	res, err := handleForkRepo(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello",
	}))
	decodeOK(t, res, err, &got)

	if !got.Pending {
		t.Error("pending = false for a 202, want true")
	}
}

func TestHandleForkRepoReportsAPIErrors(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/forks", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusForbidden, "Forking is disabled for this repository")
	})

	res, err := handleForkRepo(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello",
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "Forking is disabled") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

func TestHandleRepoToolsRequireTheirArguments(t *testing.T) {
	cases := []struct {
		name    string
		handler handlerFn
		args    map[string]any
		missing string
	}{
		{"list_tags without repo", handleListTags, map[string]any{"owner": "o"}, "repo"},
		{"list_releases without owner", handleListReleases, map[string]any{"repo": "r"}, "owner"},
		{"list_commits without owner", handleListCommits, map[string]any{"repo": "r"}, "owner"},
		{"get_commit without sha", handleGetCommit, map[string]any{"owner": "o", "repo": "r"}, "sha"},
		{"fork_repo without repo", handleForkRepo, map[string]any{"owner": "o"}, "repo"},
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

func TestSummaryHelpersOnNil(t *testing.T) {
	if got := repoSummaryFrom(nil); got.Name != "" || got.Stars != 0 {
		t.Errorf("repoSummaryFrom(nil) = %+v, want the zero value", got)
	}
	if got := commitSummaryFrom(nil); got.SHA != "" || got.Message != "" {
		t.Errorf("commitSummaryFrom(nil) = %+v, want the zero value", got)
	}
}
