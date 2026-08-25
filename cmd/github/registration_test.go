package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kahz12/droidmcp/internal/core"
)

// The published schema is the only thing a calling agent sees. This pins the
// full tool surface: a tool that stops being registered, or loses a required
// argument, breaks here rather than at the far end of an agent session.
func TestRegisterToolsPublishesTheWholeSurface(t *testing.T) {
	want := map[string][]string{
		// repo
		"list_repos":    nil,
		"get_repo":      {"owner", "repo"},
		"list_branches": {"owner", "repo"},
		"list_tags":     {"owner", "repo"},
		"list_releases": {"owner", "repo"},
		"list_commits":  {"owner", "repo"},
		"get_commit":    {"owner", "repo", "sha"},
		"fork_repo":     {"owner", "repo"},
		// issues
		"create_issue":  {"owner", "repo", "title"},
		"list_issues":   {"owner", "repo"},
		"comment_issue": {"owner", "repo", "number", "body"},
		"close_issue":   {"owner", "repo", "number"},
		"label_issue":   {"owner", "repo", "number", "labels"},
		// pull requests
		"get_pr":    {"owner", "repo", "number"},
		"create_pr": {"owner", "repo", "title", "head", "base"},
		"review_pr": {"owner", "repo", "number", "event"},
		"merge_pr":  {"owner", "repo", "number"},
		// files
		"get_file":    {"owner", "repo", "path"},
		"commit_file": {"owner", "repo", "path", "content", "message"},
		// search
		"search_code":   {"query"},
		"search_issues": {"query"},
	}

	s := core.NewDroidServer("mcp-github", "test")
	registerTools(s)
	registered := s.MCPServer.ListTools()

	if len(registered) != len(want) {
		t.Errorf("registered %d tools, want %d", len(registered), len(want))
	}

	for name, required := range want {
		t.Run(name, func(t *testing.T) {
			st, ok := registered[name]
			if !ok {
				t.Fatalf("tool %q is not registered", name)
			}
			for _, arg := range required {
				if _, ok := st.Tool.InputSchema.Properties[arg]; !ok {
					t.Errorf("%s: %q missing from the schema", name, arg)
				}
				if !slices.Contains(st.Tool.InputSchema.Required, arg) {
					t.Errorf("%s: %q should be required", name, arg)
				}
			}
			if len(st.Tool.InputSchema.Required) != len(required) {
				t.Errorf("%s: required = %v, want exactly %v", name, st.Tool.InputSchema.Required, required)
			}
			if strings.TrimSpace(st.Tool.Description) == "" {
				t.Errorf("%s: has no description", name)
			}
		})
	}
}

func TestValidateTokenAcceptsAWorkingToken(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"login": "kahz12"})
	})

	if err := validateToken(context.Background(), ghClient, "GITHUB_TOKEN"); err != nil {
		t.Errorf("validateToken on a good token = %v, want nil", err)
	}
}

// A bad token must stop the server at startup instead of failing every tool
// call later, so the error has to name the source variable.
func TestValidateTokenRejectsABadToken(t *testing.T) {
	mux, cleanup := newTestClient(t)
	defer cleanup()

	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		writeGHError(w, http.StatusUnauthorized, "Bad credentials")
	})

	err := validateToken(context.Background(), ghClient, "GITHUB_TOKEN")
	if err == nil {
		t.Fatal("validateToken on a 401 returned nil")
	}
	if !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("error = %q, want GitHub's message", err)
	}
}
