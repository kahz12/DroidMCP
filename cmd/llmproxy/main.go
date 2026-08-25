// Command llmproxy provides an MCP server that fronts a local LLM runtime
// (Ollama) so an agent can list models, generate text, embed text and inspect
// model metadata without leaving the device. No inference happens here: this is
// a thin, guarded proxy over the daemon's HTTP API.
//
// The daemon address comes from DROIDMCP_OLLAMA_HOST and never from a tool
// argument, so a calling model cannot redirect prompts elsewhere. Pointing it
// off the local network requires DROIDMCP_LLMPROXY_ALLOW_REMOTE.
package main

import (
	"os"
	"strings"

	"github.com/kahz12/droidmcp/internal/buildinfo"
	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	envOllamaHost  = "DROIDMCP_OLLAMA_HOST"
	envAllowRemote = "DROIDMCP_LLMPROXY_ALLOW_REMOTE"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	// Fail fast on a bad or off-device daemon address: every tool depends on it,
	// and a startup error is far easier to act on than four identical tool
	// failures later.
	ollama, err = resolveOllama()
	if err != nil {
		logger.Log.Error("mcp-llm-proxy cannot use the configured Ollama host. Refusing to start.", "error", err)
		os.Exit(1)
	}

	// No key required: this server reads no device data and writes nothing. It
	// does spend CPU and battery, so an API key is still recommended on a shared
	// device and is honoured when set.
	server := core.NewDroidServer("mcp-llm-proxy", buildinfo.Version)
	server.APIKey = config.ResolveAPIKey("llmproxy")
	registerTools(server)

	logger.Info("Ollama backend configured", "host", ollama.base.String())

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

// resolveOllama builds the shared client from the environment, rejecting a host
// that cannot be parsed or that would send prompts off-device.
func resolveOllama() (*client, error) {
	base, err := parseBaseURL(os.Getenv(envOllamaHost))
	if err != nil {
		return nil, err
	}
	allowRemote := allowRemoteHost()
	if err := validateBase(base, allowRemote); err != nil {
		return nil, err
	}
	return newClient(base, allowRemote), nil
}

// allowRemoteHost reports whether the operator opted in to a non-local daemon.
func allowRemoteHost() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(envAllowRemote)))
	return v == "1" || v == "true" || v == "yes"
}

// registerTools maps MCP tool definitions to their Go handlers.
func registerTools(s *core.DroidServer) {
	// list_models: what the local daemon can actually run.
	s.MCPServer.AddTool(mcp.NewTool("list_models",
		mcp.WithDescription("List the models installed in the local Ollama instance. Returns {count, models:[{name, size_bytes, family, parameter_size, quantization, modified_at}]}. Call this first: `model` arguments elsewhere must match one of these names exactly."),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 900s")),
	), handleListModels)

	// generate: single-shot completion, no streaming.
	s.MCPServer.AddTool(mcp.NewTool("generate",
		mcp.WithDescription("Generate text with a local model and return the whole completion at once (no streaming). Returns {model, response, done_reason, prompt_eval_count, eval_count, total_duration_ms, tokens_per_second}. On-device generation is slow: expect seconds to minutes."),
		mcp.WithString("model", mcp.Required(), mcp.Description("Model name as reported by list_models (e.g. \"qwen2.5:0.5b\")")),
		mcp.WithString("prompt", mcp.Required(), mcp.Description("The prompt to complete")),
		mcp.WithString("system", mcp.Description("System prompt that overrides the one baked into the model")),
		mcp.WithNumber("temperature", mcp.Description("Sampling temperature. Omit to use the model's own default")),
		mcp.WithNumber("num_predict", mcp.Description("Maximum tokens to generate. Omit to use the model's own default")),
		mcp.WithString("format", mcp.Description("Set to \"json\" to constrain the output to valid JSON. Omit otherwise")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 300s, max 900s")),
	), handleGenerate)

	// embed: one vector per call.
	s.MCPServer.AddTool(mcp.NewTool("embed",
		mcp.WithDescription("Generate an embedding vector for a piece of text using a local embedding model (e.g. nomic-embed-text). Returns {model, dimensions, embedding}."),
		mcp.WithString("model", mcp.Required(), mcp.Description("Embedding model name as reported by list_models")),
		mcp.WithString("prompt", mcp.Required(), mcp.Description("Text to embed")),
		mcp.WithBoolean("include_vector", mcp.Description("Include the vector itself in the result. Default true; set false when only the dimensions matter, since a large vector is a heavy payload")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 60s, max 900s")),
	), handleEmbed)

	// model_info: metadata and capabilities of one model.
	s.MCPServer.AddTool(mcp.NewTool("model_info",
		mcp.WithDescription("Describe one local model: {model, family, families, parameter_size, quantization, context_length, capabilities}. Use it to check the context window before sending a long prompt."),
		mcp.WithString("model", mcp.Required(), mcp.Description("Model name as reported by list_models")),
		mcp.WithBoolean("verbose", mcp.Description("Also return the raw modelfile, template, parameters and license. Default false: those fields are long and rarely useful to an agent")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 900s")),
	), handleModelInfo)
}
