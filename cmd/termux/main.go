// Command termux provides an MCP server for native Android/Termux interaction.
// Every tool routes through runCommand (exec.go) which enforces an optional
// allowlist (DROIDMCP_TERMUX_ALLOWLIST), per-call timeouts, byte caps on
// stdout/stderr, and SIGTERM-on-cancel.
package main

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
)

var cfg *config.Config

func main() {
	var err error
	cfg, err = config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	// Even with the allowlist, mcp-termux exposes process-level access to
	// the host. Refuse to start without an API key so anything else on
	// localhost (other apps, adb) cannot just connect.
	apiKey := config.ResolveAPIKey("termux")
	if apiKey == "" {
		logger.Log.Error("mcp-termux requires DROIDMCP_TERMUX_KEY or DROIDMCP_API_KEY to be set. Refusing to start.")
		os.Exit(1)
	}

	server := core.NewDroidServer("mcp-termux", "1.0.0")
	server.APIKey = apiKey
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	register(s, slices.Concat(shellTools(), jobTools(), apiTools()))
}

func handleRunCommand(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	command, err := req.RequireString("command")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	opts := execOptions{
		Command:  command,
		Args:     req.GetStringSlice("args", nil),
		Cwd:      req.GetString("cwd", ""),
		EnvExtra: stringMapArg(req, "env_extra"),
		Timeout:  timeoutFromReq(req),
	}
	return runAndRender(ctx, opts)
}

func handleInstallPkg(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pkgName, err := req.RequireString("package")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// `--` prevents pkg from interpreting a name beginning with `-` as a flag.
	return runAndRender(ctx, execOptions{
		Command: "pkg",
		Args:    []string{"install", "-y", "--", pkgName},
		Timeout: timeoutFromReq(req),
		Trusted: true,
	})
}

func handleListPkgs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return runAndRender(ctx, execOptions{
		Command: "pkg",
		Args:    []string{"list-installed"},
		Trusted: true,
	})
}

// envResult is the JSON shape for read_env. Either Name+Value (single
// lookup) or Vars (full dump) is populated.
type envResult struct {
	Name  string            `json:"name,omitempty"`
	Value string            `json:"value,omitempty"`
	Vars  map[string]string `json:"vars,omitempty"`
}

func handleReadEnv(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	if name != "" {
		return jsonResult(envResult{Name: name, Value: os.Getenv(name)})
	}
	all := make(map[string]string, len(os.Environ()))
	for _, e := range os.Environ() {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				all[e[:i]] = e[i+1:]
				break
			}
		}
	}
	return jsonResult(envResult{Vars: all})
}

func handleBatteryStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return runAndRender(ctx, execOptions{
		Command: "termux-battery-status",
		Timeout: timeoutFromReq(req),
		Trusted: true,
	})
}

func handleLocation(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := []string{}
	if p := req.GetString("provider", ""); p != "" {
		args = append(args, "-p", p)
	}
	if r := req.GetString("request", ""); r != "" {
		args = append(args, "-r", r)
	}
	return runAndRender(ctx, execOptions{
		Command: "termux-location",
		Args:    args,
		Timeout: timeoutFromReq(req),
		Trusted: true,
	})
}

func handleNotification(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := []string{}
	if t := req.GetString("title", ""); t != "" {
		args = append(args, "--title", t)
	}
	if c := req.GetString("content", ""); c != "" {
		args = append(args, "--content", c)
	}
	if id := req.GetString("id", ""); id != "" {
		args = append(args, "--id", id)
	}
	return runAndRender(ctx, execOptions{
		Command: "termux-notification",
		Args:    args,
		Trusted: true,
	})
}

func handleToast(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return runAndRender(ctx, execOptions{
		Command: "termux-toast",
		Stdin:   text,
		Trusted: true,
	})
}

func handleSMSSend(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	number, err := req.RequireString("number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// `--` separates flags from the recipient list to neutralise any
	// number that starts with `-`.
	return runAndRender(ctx, execOptions{
		Command: "termux-sms-send",
		Args:    []string{"-n", number, "--", text},
		Trusted: true,
	})
}

func handleTTSSpeak(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	args := []string{}
	if l := req.GetString("language", ""); l != "" {
		args = append(args, "-l", l)
	}
	if r := req.GetFloat("rate", 0); r > 0 {
		args = append(args, "-r", formatFloat(r))
	}
	if p := req.GetFloat("pitch", 0); p > 0 {
		args = append(args, "-p", formatFloat(p))
	}
	return runAndRender(ctx, execOptions{
		Command: "termux-tts-speak",
		Args:    args,
		Stdin:   text,
		Trusted: true,
	})
}

// runAndRender is the common tail used by every handler: invoke runCommand
// and turn its result into a JSON tool response, marking failures (and
// non-zero exit codes) as error results so the MCP client can short-circuit.
func runAndRender(ctx context.Context, opts execOptions) (*mcp.CallToolResult, error) {
	res, err := runCommand(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, mErr := json.Marshal(res)
	if mErr != nil {
		return mcp.NewToolResultError(mErr.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		return mcp.NewToolResultError(string(body)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// timeoutFromReq pulls timeout_seconds from the request and clamps to the
// allowed range. 0 means "let runCommand pick the default".
func timeoutFromReq(req mcp.CallToolRequest) time.Duration {
	t := req.GetInt("timeout_seconds", 0)
	if t <= 0 {
		return 0
	}
	if t >= int(maxExecTimeout/time.Second) {
		return maxExecTimeout
	}
	return time.Duration(t) * time.Second
}

// stringMapArg pulls a string->string map out of a JSON-decoded object arg,
// dropping non-string values. Mirrors the helper in cmd/scraper.
func stringMapArg(req mcp.CallToolRequest, name string) map[string]string {
	args := req.GetArguments()
	v, ok := args[name]
	if !ok || v == nil {
		return nil
	}
	raw, ok := v.(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, val := range raw {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func formatFloat(f float64) string {
	// Just enough precision for TTS rate/pitch; avoids importing strconv
	// imports across the file for this single call site.
	return strconv.FormatFloat(f, 'f', -1, 64)
}
