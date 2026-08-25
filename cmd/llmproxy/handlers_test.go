package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// callRequest builds an mcp.CallToolRequest with the given arguments map.
func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	}
}

// resultText concatenates all text-content blocks and reports the IsError flag.
func resultText(t *testing.T, res *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

type handlerFunc func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

// okJSON runs a handler, asserts it succeeded, and decodes its JSON payload.
func okJSON(t *testing.T, h handlerFunc, args map[string]any) map[string]any {
	t.Helper()
	res, err := h(context.Background(), callRequest(args))
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error result: %s", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result is not JSON (%v): %s", err, text)
	}
	return out
}

// errText runs a handler and asserts it produced an error result.
func errText(t *testing.T, h handlerFunc, args map[string]any) string {
	t.Helper()
	res, err := h(context.Background(), callRequest(args))
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatalf("expected an error result, got: %s", text)
	}
	return text
}

// withStubOllama points the package client at a stub daemon for one test.
func withStubOllama(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	prev := ollama
	ollama = newTestClient(t, handler)
	t.Cleanup(func() { ollama = prev })
}

// decodeBody reads the request body of a stub handler as a generic map.
func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("request body is not JSON (%v): %s", err, raw)
	}
	return out
}

func TestListModelsSummarizesTags(t *testing.T) {
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q, want /api/tags", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"models":[
			{"name":"qwen2.5:0.5b","size":397807936,"modified_at":"2026-08-01T10:00:00Z",
			 "details":{"family":"qwen2","parameter_size":"0.5B","quantization_level":"Q4_K_M"}},
			{"name":"nomic-embed-text:latest","size":274302450,"modified_at":"2026-07-20T09:00:00Z",
			 "details":{"family":"nomic-bert","parameter_size":"137M","quantization_level":"F16"}}
		]}`)
	})

	out := okJSON(t, handleListModels, nil)

	if out["count"] != float64(2) {
		t.Errorf("count = %v, want 2", out["count"])
	}
	models, ok := out["models"].([]any)
	if !ok || len(models) != 2 {
		t.Fatalf("models = %v, want a 2-element array", out["models"])
	}
	first, _ := models[0].(map[string]any)
	if first["name"] != "qwen2.5:0.5b" {
		t.Errorf("name = %v, want qwen2.5:0.5b", first["name"])
	}
	if first["parameter_size"] != "0.5B" {
		t.Errorf("parameter_size = %v, want 0.5B", first["parameter_size"])
	}
	if first["quantization"] != "Q4_K_M" {
		t.Errorf("quantization = %v, want Q4_K_M", first["quantization"])
	}
	if first["family"] != "qwen2" {
		t.Errorf("family = %v, want qwen2", first["family"])
	}
	if first["size_bytes"] != float64(397807936) {
		t.Errorf("size_bytes = %v, want 397807936", first["size_bytes"])
	}
}

func TestListModelsOnEmptyLibrary(t *testing.T) {
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"models":[]}`)
	})

	out := okJSON(t, handleListModels, nil)
	if out["count"] != float64(0) {
		t.Errorf("count = %v, want 0", out["count"])
	}
	if models, ok := out["models"].([]any); !ok || len(models) != 0 {
		t.Errorf("models = %v, want an empty array (not null)", out["models"])
	}
}

func TestGenerateSendsPromptAndMapsMetrics(t *testing.T) {
	var body map[string]any
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("path = %q, want /api/generate", r.URL.Path)
		}
		body = decodeBody(t, r)
		_, _ = io.WriteString(w, `{"model":"qwen2.5:0.5b","response":"Hola","done":true,
			"done_reason":"stop","total_duration":3000000000,"prompt_eval_count":7,
			"eval_count":10,"eval_duration":2000000000}`)
	})

	out := okJSON(t, handleGenerate, map[string]any{"model": "qwen2.5:0.5b", "prompt": "di hola"})

	if body["model"] != "qwen2.5:0.5b" {
		t.Errorf("request model = %v, want qwen2.5:0.5b", body["model"])
	}
	if body["prompt"] != "di hola" {
		t.Errorf("request prompt = %v, want the prompt", body["prompt"])
	}
	if body["stream"] != false {
		t.Errorf("request stream = %v, want false", body["stream"])
	}
	if out["response"] != "Hola" {
		t.Errorf("response = %v, want Hola", out["response"])
	}
	if out["done_reason"] != "stop" {
		t.Errorf("done_reason = %v, want stop", out["done_reason"])
	}
	if out["eval_count"] != float64(10) {
		t.Errorf("eval_count = %v, want 10", out["eval_count"])
	}
	if out["prompt_eval_count"] != float64(7) {
		t.Errorf("prompt_eval_count = %v, want 7", out["prompt_eval_count"])
	}
	if out["total_duration_ms"] != float64(3000) {
		t.Errorf("total_duration_ms = %v, want 3000", out["total_duration_ms"])
	}
	// 10 tokens over 2 seconds of eval time.
	if out["tokens_per_second"] != float64(5) {
		t.Errorf("tokens_per_second = %v, want 5", out["tokens_per_second"])
	}
}

func TestGenerateForwardsSamplingOptions(t *testing.T) {
	var body map[string]any
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		_, _ = io.WriteString(w, `{"response":"ok"}`)
	})

	okJSON(t, handleGenerate, map[string]any{
		"model":       "qwen2.5:0.5b",
		"prompt":      "hola",
		"system":      "responde en catalán",
		"temperature": 0.2,
		"num_predict": 64,
	})

	if body["system"] != "responde en catalán" {
		t.Errorf("system = %v, want the system prompt", body["system"])
	}
	opts, ok := body["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %v, want an object", body["options"])
	}
	if opts["temperature"] != 0.2 {
		t.Errorf("options.temperature = %v, want 0.2", opts["temperature"])
	}
	if opts["num_predict"] != float64(64) {
		t.Errorf("options.num_predict = %v, want 64", opts["num_predict"])
	}
}

// Sending options the caller never set would override the model's own defaults
// with zeros, so the key must be absent entirely.
func TestGenerateOmitsOptionsWhenUnset(t *testing.T) {
	var body map[string]any
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		_, _ = io.WriteString(w, `{"response":"ok"}`)
	})

	okJSON(t, handleGenerate, map[string]any{"model": "qwen2.5:0.5b", "prompt": "hola"})

	if _, present := body["options"]; present {
		t.Errorf("options = %v, want the key to be absent", body["options"])
	}
	if _, present := body["system"]; present {
		t.Errorf("system = %v, want the key to be absent", body["system"])
	}
	if _, present := body["format"]; present {
		t.Errorf("format = %v, want the key to be absent", body["format"])
	}
}

// A client that serializes omitted optional numbers as JSON null must not be
// read as "the caller set this to zero": num_predict 0 would return an empty
// completion for no visible reason.
func TestGenerateTreatsNullOptionsAsUnset(t *testing.T) {
	var body map[string]any
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		_, _ = io.WriteString(w, `{"response":"ok"}`)
	})

	okJSON(t, handleGenerate, map[string]any{
		"model":       "qwen2.5:0.5b",
		"prompt":      "hola",
		"temperature": nil,
		"num_predict": nil,
		"system":      nil,
		"format":      nil,
	})

	if _, present := body["options"]; present {
		t.Errorf("options = %v, want the key to be absent for null arguments", body["options"])
	}
	if _, present := body["system"]; present {
		t.Errorf("system = %v, want the key to be absent for a null argument", body["system"])
	}
}

func TestGenerateAcceptsJSONFormat(t *testing.T) {
	var body map[string]any
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		_, _ = io.WriteString(w, `{"response":"{}"}`)
	})

	okJSON(t, handleGenerate, map[string]any{"model": "m", "prompt": "p", "format": "json"})

	if body["format"] != "json" {
		t.Errorf("format = %v, want json", body["format"])
	}
}

func TestGenerateRejectsUnknownFormat(t *testing.T) {
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler reached the daemon despite an invalid format")
	})

	got := errText(t, handleGenerate, map[string]any{"model": "m", "prompt": "p", "format": "yaml"})
	if !strings.Contains(got, "json") {
		t.Errorf("error = %q, want it to name the only supported format", got)
	}
}

func TestGenerateRequiresModelAndPrompt(t *testing.T) {
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler reached the daemon despite missing arguments")
	})

	if got := errText(t, handleGenerate, map[string]any{"prompt": "hola"}); !strings.Contains(got, "model") {
		t.Errorf("error = %q, want it to name the missing model argument", got)
	}
	if got := errText(t, handleGenerate, map[string]any{"model": "m"}); !strings.Contains(got, "prompt") {
		t.Errorf("error = %q, want it to name the missing prompt argument", got)
	}
}

func TestGenerateReportsDaemonErrors(t *testing.T) {
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"model 'ghost' not found"}`)
	})

	got := errText(t, handleGenerate, map[string]any{"model": "ghost", "prompt": "hola"})
	if !strings.Contains(got, "ghost") {
		t.Errorf("error = %q, want it to carry the daemon's message", got)
	}
}

func TestEmbedReturnsVectorAndDimensions(t *testing.T) {
	var body map[string]any
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("path = %q, want /api/embeddings", r.URL.Path)
		}
		body = decodeBody(t, r)
		_, _ = io.WriteString(w, `{"embedding":[0.1,-0.2,0.3]}`)
	})

	out := okJSON(t, handleEmbed, map[string]any{"model": "nomic-embed-text", "prompt": "hola"})

	if body["prompt"] != "hola" {
		t.Errorf("request prompt = %v, want hola", body["prompt"])
	}
	if out["dimensions"] != float64(3) {
		t.Errorf("dimensions = %v, want 3", out["dimensions"])
	}
	vec, ok := out["embedding"].([]any)
	if !ok || len(vec) != 3 {
		t.Fatalf("embedding = %v, want a 3-element array", out["embedding"])
	}
	if vec[1] != -0.2 {
		t.Errorf("embedding[1] = %v, want -0.2", vec[1])
	}
}

// A 4096-float vector is expensive to push through an agent's context, so a
// caller that only needs the size can drop it.
func TestEmbedCanOmitTheVector(t *testing.T) {
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"embedding":[0.1,0.2,0.3,0.4]}`)
	})

	out := okJSON(t, handleEmbed, map[string]any{
		"model":          "nomic-embed-text",
		"prompt":         "hola",
		"include_vector": false,
	})

	if out["dimensions"] != float64(4) {
		t.Errorf("dimensions = %v, want 4", out["dimensions"])
	}
	if _, present := out["embedding"]; present {
		t.Errorf("embedding = %v, want the key to be absent", out["embedding"])
	}
}

func TestEmbedRejectsEmptyEmbedding(t *testing.T) {
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"embedding":[]}`)
	})

	got := errText(t, handleEmbed, map[string]any{"model": "chat-only-model", "prompt": "hola"})
	if !strings.Contains(got, "embedding") {
		t.Errorf("error = %q, want it to explain the model returned no embedding", got)
	}
}

func TestModelInfoSummarizesShow(t *testing.T) {
	var body map[string]any
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Errorf("path = %q, want /api/show", r.URL.Path)
		}
		body = decodeBody(t, r)
		_, _ = io.WriteString(w, `{
			"license":"MIT","modelfile":"FROM /root/.ollama/blobs/sha256-abc","template":"{{ .Prompt }}",
			"parameters":"stop \"<|im_end|>\"",
			"details":{"family":"qwen2","families":["qwen2"],"parameter_size":"0.5B","quantization_level":"Q4_K_M"},
			"model_info":{"general.architecture":"qwen2","qwen2.context_length":32768,"qwen2.block_count":24},
			"capabilities":["completion","tools"]
		}`)
	})

	out := okJSON(t, handleModelInfo, map[string]any{"model": "qwen2.5:0.5b"})

	if body["model"] != "qwen2.5:0.5b" {
		t.Errorf("request model = %v, want qwen2.5:0.5b", body["model"])
	}
	if out["family"] != "qwen2" {
		t.Errorf("family = %v, want qwen2", out["family"])
	}
	if out["parameter_size"] != "0.5B" {
		t.Errorf("parameter_size = %v, want 0.5B", out["parameter_size"])
	}
	if out["quantization"] != "Q4_K_M" {
		t.Errorf("quantization = %v, want Q4_K_M", out["quantization"])
	}
	if out["context_length"] != float64(32768) {
		t.Errorf("context_length = %v, want 32768", out["context_length"])
	}
	caps, ok := out["capabilities"].([]any)
	if !ok || len(caps) != 2 {
		t.Fatalf("capabilities = %v, want 2 entries", out["capabilities"])
	}
	// The modelfile and license are kilobytes of noise for a calling agent.
	if _, present := out["modelfile"]; present {
		t.Error("modelfile present without verbose; want it omitted")
	}
	if _, present := out["license"]; present {
		t.Error("license present without verbose; want it omitted")
	}
}

func TestModelInfoVerboseIncludesRawFields(t *testing.T) {
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"modelfile":"FROM blob","template":"{{ .Prompt }}",
			"license":"MIT","parameters":"stop x","details":{"family":"qwen2"},"model_info":{}}`)
	})

	out := okJSON(t, handleModelInfo, map[string]any{"model": "qwen2.5:0.5b", "verbose": true})

	if out["modelfile"] != "FROM blob" {
		t.Errorf("modelfile = %v, want it included under verbose", out["modelfile"])
	}
	if out["template"] != "{{ .Prompt }}" {
		t.Errorf("template = %v, want it included under verbose", out["template"])
	}
	if out["license"] != "MIT" {
		t.Errorf("license = %v, want it included under verbose", out["license"])
	}
}

func TestModelInfoRequiresModel(t *testing.T) {
	withStubOllama(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler reached the daemon despite a missing model")
	})

	if got := errText(t, handleModelInfo, nil); !strings.Contains(got, "model") {
		t.Errorf("error = %q, want it to name the missing model argument", got)
	}
}

// The context length key is namespaced by architecture (llama.context_length,
// qwen2.context_length, ...), so it has to be found by suffix.
func TestContextLengthFindsNamespacedKey(t *testing.T) {
	cases := []struct {
		name string
		info map[string]any
		want int64
	}{
		{"qwen", map[string]any{"qwen2.context_length": float64(32768)}, 32768},
		{"llama", map[string]any{"llama.context_length": float64(8192)}, 8192},
		{"absent", map[string]any{"general.architecture": "phi3"}, 0},
		{"ignores similar keys", map[string]any{"llama.embedding_length": float64(4096)}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := contextLength(tc.info); got != tc.want {
				t.Errorf("contextLength(%v) = %d, want %d", tc.info, got, tc.want)
			}
		})
	}
}

func TestCallTimeoutClampsToBounds(t *testing.T) {
	cases := []struct {
		name string
		arg  any
		want time.Duration
	}{
		{"absent uses the default", nil, defaultGenerateTimeout},
		{"zero uses the default", 0, defaultGenerateTimeout},
		{"negative uses the default", -5, defaultGenerateTimeout},
		{"in range is honoured", 45, 45 * time.Second},
		{"above the ceiling is clamped", 99999, maxTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{}
			if tc.arg != nil {
				args["timeout_seconds"] = tc.arg
			}
			if got := callTimeout(callRequest(args), defaultGenerateTimeout); got != tc.want {
				t.Errorf("callTimeout = %v, want %v", got, tc.want)
			}
		})
	}
}
