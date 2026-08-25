package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHandleGetFileDecodesContent(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var gotRef atomic.Value
	mux.HandleFunc("/repos/octo/hello/contents/docs/readme.md", func(w http.ResponseWriter, r *http.Request) {
		gotRef.Store(r.URL.Query().Get("ref"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":     "file",
			"path":     "docs/readme.md",
			"sha":      "abc123",
			"size":     11,
			"encoding": "base64",
			"content":  base64.StdEncoding.EncodeToString([]byte("hola mundo\n")),
			"html_url": "https://github.com/octo/hello/blob/main/docs/readme.md",
		})
	})

	var got fileResult
	res, err := handleGetFile(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "path": "docs/readme.md", "ref": "release",
	}))
	decodeOK(t, res, err, &got)

	if got.Content != "hola mundo\n" {
		t.Errorf("content = %q, want the decoded text", got.Content)
	}
	if got.SHA != "abc123" || got.Path != "docs/readme.md" || got.Size != 11 {
		t.Errorf("unexpected file result: %+v", got)
	}
	if ref, _ := gotRef.Load().(string); ref != "release" {
		t.Errorf("ref sent to the api = %q, want release", ref)
	}
}

func TestHandleGetFileOnADirectory(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	// A directory comes back as a JSON array, which go-github reports as
	// directory content with a nil file.
	mux.HandleFunc("/repos/octo/hello/contents/docs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"type": "file", "path": "docs/readme.md", "name": "readme.md"},
		})
	})

	res, err := handleGetFile(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "path": "docs",
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "directory") {
		t.Errorf("error = %q, want it to say the path is a directory", text)
	}
}

func TestHandleGetFileReportsAPIErrors(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/contents/nope.md", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusNotFound, "Not Found")
	})

	res, err := handleGetFile(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "path": "nope.md",
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "Not Found") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

// A missing file is a create: no SHA may be sent, or the API rejects the call.
func TestHandleCommitFileCreatesWhenAbsent(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var putBody atomic.Value
	mux.HandleFunc("/repos/octo/hello/contents/new.txt", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeGHError(w, http.StatusNotFound, "Not Found")
			return
		}
		putBody.Store(requestBody(t, r))
		// go-github's RepositoryContentResponse embeds Commit, so the sha and
		// html_url the handler reports are the commit's, not the blob's.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"sha": "blobsha", "path": "new.txt"},
			"commit": map[string]any{
				"sha":      "commitsha",
				"html_url": "https://github.com/octo/hello/commit/commitsha",
			},
		})
	})

	var got commitFileResult
	res, err := handleCommitFile(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "path": "new.txt",
		"content": "hola", "message": "add new.txt",
	}))
	decodeOK(t, res, err, &got)

	if !got.Created {
		t.Error("created = false, want true for a file that did not exist")
	}
	if got.SHA != "commitsha" {
		t.Errorf("sha = %q, want the commit sha (not the blob sha)", got.SHA)
	}
	body, _ := putBody.Load().(map[string]any)
	if body == nil {
		t.Fatal("the handler never issued the PUT")
	}
	if _, present := body["sha"]; present {
		t.Errorf("PUT body carries sha=%v; a create must not send one", body["sha"])
	}
	if body["message"] != "add new.txt" {
		t.Errorf("commit message = %v, want the one passed in", body["message"])
	}
	if decoded, _ := base64.StdEncoding.DecodeString(asString(body["content"])); string(decoded) != "hola" {
		t.Errorf("committed content = %q, want hola", decoded)
	}
}

// An existing file is an update: the current SHA is mandatory, and the branch
// must be forwarded or the commit lands on the default branch.
func TestHandleCommitFileUpdatesExistingOnTheGivenBranch(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	var getRef, putBody atomic.Value
	mux.HandleFunc("/repos/octo/hello/contents/README.md", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			getRef.Store(r.URL.Query().Get("ref"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type": "file", "path": "README.md", "sha": "oldsha", "size": 3,
				"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("old")),
			})
			return
		}
		putBody.Store(requestBody(t, r))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"sha": "newblobsha"},
			"commit":  map[string]any{"sha": "updatecommitsha"},
		})
	})

	var got commitFileResult
	res, err := handleCommitFile(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "path": "README.md",
		"content": "nuevo", "message": "update", "branch": "feature",
	}))
	decodeOK(t, res, err, &got)

	if got.Created {
		t.Error("created = true, want false for an existing file")
	}
	if got.SHA != "updatecommitsha" {
		t.Errorf("sha = %q, want the commit sha (not the blob sha)", got.SHA)
	}
	if ref, _ := getRef.Load().(string); ref != "feature" {
		t.Errorf("lookup ref = %q, want feature", ref)
	}
	body, _ := putBody.Load().(map[string]any)
	if body == nil {
		t.Fatal("the handler never issued the PUT")
	}
	if body["sha"] != "oldsha" {
		t.Errorf("PUT sha = %v, want the existing oldsha", body["sha"])
	}
	if body["branch"] != "feature" {
		t.Errorf("PUT branch = %v, want feature; without it the commit lands on the default branch", body["branch"])
	}
}

// The lookup failing for any reason other than 404 must not be read as "the
// file does not exist": committing then would overwrite it with no SHA.
func TestHandleCommitFileRefusesWhenTheLookupFails(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	putCalled := false
	mux.HandleFunc("/repos/octo/hello/contents/README.md", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeGHError(w, http.StatusInternalServerError, "Server Error")
			return
		}
		putCalled = true
	})

	res, err := handleCommitFile(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "path": "README.md",
		"content": "x", "message": "m",
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "checking existing file") {
		t.Errorf("error = %q, want it to name the failed lookup", text)
	}
	if putCalled {
		t.Error("the handler committed anyway after a failed lookup")
	}
}

func TestHandleCommitFileOnADirectory(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	putCalled := false
	mux.HandleFunc("/repos/octo/hello/contents/docs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "path": "docs/a.md"}})
			return
		}
		putCalled = true
	})

	res, err := handleCommitFile(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "path": "docs",
		"content": "x", "message": "m",
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "directory") {
		t.Errorf("error = %q, want it to say the path is a directory", text)
	}
	if putCalled {
		t.Error("the handler tried to commit over a directory")
	}
}

func TestHandleCommitFileReportsAPIErrors(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/repos/octo/hello/contents/locked.txt", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeGHError(w, http.StatusNotFound, "Not Found")
			return
		}
		writeGHError(w, http.StatusForbidden, "Resource not accessible by integration")
	})

	res, err := handleCommitFile(context.Background(), callRequest(map[string]any{
		"owner": "octo", "repo": "hello", "path": "locked.txt",
		"content": "x", "message": "m",
	}))
	text := mustErr(t, res, err)

	if !strings.Contains(text, "not accessible") {
		t.Errorf("error = %q, want GitHub's message", text)
	}
}

func TestHandleFileToolsRequireTheirArguments(t *testing.T) {
	cases := []struct {
		name    string
		handler handlerFn
		args    map[string]any
		missing string
	}{
		{"get_file without owner", handleGetFile, map[string]any{"repo": "r", "path": "p"}, "owner"},
		{"get_file without path", handleGetFile, map[string]any{"owner": "o", "repo": "r"}, "path"},
		{"commit_file without content", handleCommitFile,
			map[string]any{"owner": "o", "repo": "r", "path": "p", "message": "m"}, "content"},
		{"commit_file without message", handleCommitFile,
			map[string]any{"owner": "o", "repo": "r", "path": "p", "content": "c"}, "message"},
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

// asString is a small reader for values decoded out of a JSON request body.
func asString(v any) string {
	s, _ := v.(string)
	return s
}
