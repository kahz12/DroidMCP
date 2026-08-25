// Tool handlers. Each one maps an MCP call onto a single Ollama endpoint and
// summarizes the reply: the daemon's raw payloads carry kilobytes of modelfile
// and template text that would only burn context in the calling agent, so the
// handlers keep the fields an agent can act on and hide the rest behind a
// verbose flag.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ollama is the shared client, wired up in main and swapped by tests.
var ollama *client

// Per-tool timeout defaults. Generation on a phone is slow enough that minutes
// are normal, while listing tags should fail fast when the daemon is down.
const (
	defaultListTimeout     = 15 * time.Second
	defaultGenerateTimeout = 300 * time.Second
	defaultEmbedTimeout    = 60 * time.Second
	maxTimeout             = 900 * time.Second
)

// callTimeout reads the optional timeout_seconds argument, falling back to def
// and clamping to maxTimeout.
func callTimeout(req mcp.CallToolRequest, def time.Duration) time.Duration {
	d := time.Duration(req.GetInt("timeout_seconds", 0)) * time.Second
	if d <= 0 {
		d = def
	}
	if d > maxTimeout {
		d = maxTimeout
	}
	return d
}

func handleListModels(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout(req, defaultListTimeout))
	defer cancel()

	var resp struct {
		Models []struct {
			Name       string `json:"name"`
			Size       int64  `json:"size"`
			ModifiedAt string `json:"modified_at"`
			Details    struct {
				Family            string `json:"family"`
				ParameterSize     string `json:"parameter_size"`
				QuantizationLevel string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := ollama.get(callCtx, "/api/tags", &resp); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	models := make([]map[string]any, 0, len(resp.Models))
	for _, m := range resp.Models {
		models = append(models, map[string]any{
			"name":           m.Name,
			"size_bytes":     m.Size,
			"family":         m.Details.Family,
			"parameter_size": m.Details.ParameterSize,
			"quantization":   m.Details.QuantizationLevel,
			"modified_at":    m.ModifiedAt,
		})
	}
	return jsonResult(map[string]any{"count": len(models), "models": models})
}

func handleGenerate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	model, err := req.RequireString("model")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prompt, err := req.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	body := map[string]any{
		"model":  model,
		"prompt": prompt,
		// Streaming is deliberately off: an MCP tool call returns one payload,
		// so a stream would only have to be reassembled here anyway.
		"stream": false,
	}
	if system := strings.TrimSpace(req.GetString("system", "")); system != "" {
		body["system"] = system
	}
	if format := strings.TrimSpace(req.GetString("format", "")); format != "" {
		if format != "json" {
			return mcp.NewToolResultError(fmt.Sprintf("format %q is not supported; the only accepted value is \"json\"", format)), nil
		}
		body["format"] = format
	}

	// Only forward sampling options the caller actually set. Sending zeros for
	// the rest would silently override each model's own defaults. A JSON null
	// counts as unset: clients routinely serialize an omitted optional number
	// that way, and reading it as zero would cap num_predict at no tokens.
	args := req.GetArguments()
	options := map[string]any{}
	if v, ok := args["temperature"]; ok && v != nil {
		options["temperature"] = req.GetFloat("temperature", 0)
	}
	if v, ok := args["num_predict"]; ok && v != nil {
		options["num_predict"] = req.GetInt("num_predict", 0)
	}
	if len(options) > 0 {
		body["options"] = options
	}

	callCtx, cancel := context.WithTimeout(ctx, callTimeout(req, defaultGenerateTimeout))
	defer cancel()

	var resp struct {
		Model           string `json:"model"`
		Response        string `json:"response"`
		DoneReason      string `json:"done_reason"`
		TotalDuration   int64  `json:"total_duration"`
		LoadDuration    int64  `json:"load_duration"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		EvalCount       int    `json:"eval_count"`
		EvalDuration    int64  `json:"eval_duration"`
	}
	if err := ollama.post(callCtx, "/api/generate", body, &resp); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	out := map[string]any{
		"model":             model,
		"response":          resp.Response,
		"done_reason":       resp.DoneReason,
		"prompt_eval_count": resp.PromptEvalCount,
		"eval_count":        resp.EvalCount,
		"total_duration_ms": resp.TotalDuration / int64(time.Millisecond),
		"load_duration_ms":  resp.LoadDuration / int64(time.Millisecond),
	}
	if resp.EvalDuration > 0 && resp.EvalCount > 0 {
		tps := float64(resp.EvalCount) / (float64(resp.EvalDuration) / float64(time.Second))
		out["tokens_per_second"] = math.Round(tps*100) / 100
	}
	return jsonResult(out)
}

func handleEmbed(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	model, err := req.RequireString("model")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prompt, err := req.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	callCtx, cancel := context.WithTimeout(ctx, callTimeout(req, defaultEmbedTimeout))
	defer cancel()

	var resp struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := ollama.post(callCtx, "/api/embeddings", map[string]any{"model": model, "prompt": prompt}, &resp); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(resp.Embedding) == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("%s returned an empty embedding; it is probably not an embedding model (try nomic-embed-text or mxbai-embed-large)", model)), nil
	}

	out := map[string]any{
		"model":      model,
		"dimensions": len(resp.Embedding),
	}
	// The vector is the point of the tool, but a few thousand floats is a heavy
	// payload for an agent that only wanted its size.
	if req.GetBool("include_vector", true) {
		out["embedding"] = resp.Embedding
	}
	return jsonResult(out)
}

func handleModelInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	model, err := req.RequireString("model")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	callCtx, cancel := context.WithTimeout(ctx, callTimeout(req, defaultListTimeout))
	defer cancel()

	// "model" is the current field name; "name" is what older daemons expect.
	// Sending both keeps this working across Ollama versions.
	body := map[string]any{"model": model, "name": model}

	var resp struct {
		License    string `json:"license"`
		Modelfile  string `json:"modelfile"`
		Parameters string `json:"parameters"`
		Template   string `json:"template"`
		Details    struct {
			Family            string   `json:"family"`
			Families          []string `json:"families"`
			ParameterSize     string   `json:"parameter_size"`
			QuantizationLevel string   `json:"quantization_level"`
		} `json:"details"`
		ModelInfo    map[string]any `json:"model_info"`
		Capabilities []string       `json:"capabilities"`
	}
	if err := ollama.post(callCtx, "/api/show", body, &resp); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	out := map[string]any{
		"model":          model,
		"family":         resp.Details.Family,
		"parameter_size": resp.Details.ParameterSize,
		"quantization":   resp.Details.QuantizationLevel,
	}
	if len(resp.Details.Families) > 0 {
		out["families"] = resp.Details.Families
	}
	if n := contextLength(resp.ModelInfo); n > 0 {
		out["context_length"] = n
	}
	if len(resp.Capabilities) > 0 {
		out["capabilities"] = resp.Capabilities
	}
	if req.GetBool("verbose", false) {
		out["modelfile"] = resp.Modelfile
		out["template"] = resp.Template
		out["parameters"] = resp.Parameters
		out["license"] = resp.License
	}
	return jsonResult(out)
}

// contextLength digs the context window out of the model_info block. The key is
// namespaced by architecture (llama.context_length, qwen2.context_length, ...),
// so it can only be found by suffix.
func contextLength(info map[string]any) int64 {
	for k, v := range info {
		if !strings.HasSuffix(k, ".context_length") {
			continue
		}
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		case json.Number:
			parsed, err := n.Int64()
			if err != nil {
				return 0
			}
			return parsed
		}
	}
	return 0
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}
