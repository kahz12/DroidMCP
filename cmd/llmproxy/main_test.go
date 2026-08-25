package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/kahz12/droidmcp/internal/core"
)

// The published schema is the only thing a calling agent can see. An argument a
// handler honours but never declares is unreachable: a client that validates
// against the schema drops it.
func TestRegisteredToolSchemasMatchTheDocumentedArguments(t *testing.T) {
	cases := []struct {
		tool     string
		required []string
		optional []string
	}{
		{"list_models", nil, []string{"timeout_seconds"}},
		{"generate", []string{"model", "prompt"},
			[]string{"system", "temperature", "num_predict", "format", "timeout_seconds"}},
		{"embed", []string{"model", "prompt"}, []string{"include_vector", "timeout_seconds"}},
		{"model_info", []string{"model"}, []string{"verbose", "timeout_seconds"}},
	}

	s := core.NewDroidServer("mcp-llm-proxy", "test")
	registerTools(s)
	registered := s.MCPServer.ListTools()

	if len(registered) != len(cases) {
		t.Errorf("registered %d tools, want %d", len(registered), len(cases))
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			st, ok := registered[tc.tool]
			if !ok {
				t.Fatalf("tool %q is not registered", tc.tool)
			}
			schema := st.Tool.InputSchema
			for _, arg := range append(append([]string{}, tc.required...), tc.optional...) {
				if _, ok := schema.Properties[arg]; !ok {
					t.Errorf("%s: argument %q is missing from the published schema", tc.tool, arg)
				}
			}
			for _, arg := range tc.required {
				if !slices.Contains(schema.Required, arg) {
					t.Errorf("%s: argument %q should be required", tc.tool, arg)
				}
			}
			for _, arg := range tc.optional {
				if slices.Contains(schema.Required, arg) {
					t.Errorf("%s: argument %q should be optional", tc.tool, arg)
				}
			}
		})
	}
}

func TestResolveOllamaDefaultsToLoopback(t *testing.T) {
	t.Setenv(envOllamaHost, "")

	c, err := resolveOllama()
	if err != nil {
		t.Fatalf("resolveOllama returned error: %v", err)
	}
	if got := c.base.String(); got != defaultOllamaHost {
		t.Errorf("base = %q, want %q", got, defaultOllamaHost)
	}
}

func TestResolveOllamaHonoursTheEnvHost(t *testing.T) {
	t.Setenv(envOllamaHost, "127.0.0.1:9999")

	c, err := resolveOllama()
	if err != nil {
		t.Fatalf("resolveOllama returned error: %v", err)
	}
	if got := c.base.String(); got != "http://127.0.0.1:9999" {
		t.Errorf("base = %q, want http://127.0.0.1:9999", got)
	}
}

func TestResolveOllamaRejectsAnUnusableHost(t *testing.T) {
	t.Setenv(envOllamaHost, "ftp://127.0.0.1:11434")

	if _, err := resolveOllama(); err == nil {
		t.Fatal("resolveOllama accepted an ftp host")
	}
}

func TestResolveOllamaBlocksAnOffDeviceHost(t *testing.T) {
	t.Setenv(envOllamaHost, "http://198.51.100.7:11434")

	_, err := resolveOllama()
	if err == nil {
		t.Fatal("resolveOllama accepted a public host without the opt-in")
	}
	if !strings.Contains(err.Error(), envAllowRemote) {
		t.Errorf("error = %q, want it to name %s", err, envAllowRemote)
	}
}

func TestResolveOllamaAllowsAnOffDeviceHostWhenOptedIn(t *testing.T) {
	for _, value := range []string{"1", "true", "YES"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envOllamaHost, "http://198.51.100.7:11434")
			t.Setenv(envAllowRemote, value)

			if _, err := resolveOllama(); err != nil {
				t.Errorf("resolveOllama with %s=%s returned error: %v", envAllowRemote, value, err)
			}
		})
	}
}

func TestResolveOllamaIgnoresAnUnsetOptIn(t *testing.T) {
	t.Setenv(envOllamaHost, "http://198.51.100.7:11434")
	t.Setenv(envAllowRemote, "0")

	if _, err := resolveOllama(); err == nil {
		t.Fatal("resolveOllama treated DROIDMCP_LLMPROXY_ALLOW_REMOTE=0 as an opt-in")
	}
}
