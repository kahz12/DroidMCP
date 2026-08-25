package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHandleSearchCodeMapsHits(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var query atomic.Value
	mux.HandleFunc("/search/code", func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count":        2,
			"incomplete_results": false,
			"items": []map[string]any{
				{
					"name": "client.go", "path": "cmd/llmproxy/client.go", "sha": "aaa",
					"html_url":   "https://github.com/kahz12/droidmcp/blob/main/cmd/llmproxy/client.go",
					"repository": map[string]any{"full_name": "kahz12/droidmcp"},
				},
				{"name": "main.go", "path": "cmd/llmproxy/main.go", "sha": "bbb"},
			},
		})
	})

	var got searchResponse[codeHit]
	res, err := handleSearchCode(context.Background(), callRequest(map[string]any{
		"query": "checkDialAddress repo:kahz12/droidmcp",
		"sort":  "indexed", "order": "asc", "per_page": float64(10),
	}))
	decodeOK(t, res, err, &got)

	q, _ := query.Load().(string)
	for _, want := range []string{"q=", "sort=indexed", "order=asc", "per_page=10"} {
		if !strings.Contains(q, want) {
			t.Errorf("query = %q, missing %q", q, want)
		}
	}
	if got.Total != 2 || got.Count != 2 {
		t.Errorf("total/count = %d/%d, want 2/2", got.Total, got.Count)
	}
	if got.Items[0].Repository != "kahz12/droidmcp" {
		t.Errorf("repository = %q, want the full name flattened", got.Items[0].Repository)
	}
	if got.Items[0].Path != "cmd/llmproxy/client.go" {
		t.Errorf("path = %q", got.Items[0].Path)
	}
}

// GitHub sets incomplete_results when the search timed out, and a caller that
// misses it will read a partial answer as the whole story.
func TestHandleSearchCodeSurfacesIncompleteResults(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/search/code", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count":        1,
			"incomplete_results": true,
			"items":              []map[string]any{{"name": "a.go", "path": "a.go"}},
		})
	})

	var got searchResponse[codeHit]
	res, err := handleSearchCode(context.Background(), callRequest(map[string]any{"query": "algo"}))
	decodeOK(t, res, err, &got)

	if !got.IncompleteResults {
		t.Error("incomplete_results = false, want it carried through")
	}
}

func TestHandleSearchCodeOnNoHits(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/search/code", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 0, "items": []map[string]any{}})
	})

	var got searchResponse[codeHit]
	res, err := handleSearchCode(context.Background(), callRequest(map[string]any{"query": "nada"}))
	decodeOK(t, res, err, &got)

	if got.Count != 0 {
		t.Errorf("count = %d, want 0", got.Count)
	}
	if got.Items == nil {
		t.Error("items = null, want an empty array")
	}
}

func TestHandleSearchCodeReportsAPIErrors(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/search/code", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusUnprocessableEntity, "Validation Failed: query is too long")
	})

	res, err := handleSearchCode(context.Background(), callRequest(map[string]any{"query": "x"}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "query is too long") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

func TestHandleSearchCodeRequiresAQuery(t *testing.T) {
	res, err := handleSearchCode(context.Background(), callRequest(map[string]any{}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "query") {
		t.Errorf("error = %q, want it to name the missing query argument", text)
	}
}
