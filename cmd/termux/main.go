// Command termux provides an MCP server for native Android/Termux interaction.
// It allows shell command execution, package management, and environment inspection.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

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

	// mcp-termux exposes a shell; running it without authentication would give
	// anything on localhost (other apps, adb, etc.) full shell access. Refuse
	// to start unless an API key is configured.
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
	// run_command: Generic shell command execution.
	// CAUTION: This provides full shell access to the agent.
	runCmdTool := mcp.NewTool("run_command",
		mcp.WithDescription("Execute a command in Termux shell"),
		mcp.WithString("command", mcp.Required(), mcp.Description("The command to execute")),
		mcp.WithArray("args",
			mcp.WithStringItems(),
			mcp.Description("Arguments for the command, one element per argument (preserves spaces in individual args)"),
		),
	)
	s.MCPServer.AddTool(runCmdTool, handleRunCommand)

	// install_pkg: Wrapper for 'pkg install'.
	installPkgTool := mcp.NewTool("install_pkg",
		mcp.WithDescription("Install a package using pkg install"),
		mcp.WithString("package", mcp.Required(), mcp.Description("Name of the package to install")),
	)
	s.MCPServer.AddTool(installPkgTool, handleInstallPkg)

	// list_pkgs: Returns currently installed Termux packages.
	listPkgsTool := mcp.NewTool("list_pkgs",
		mcp.WithDescription("List installed packages"),
	)
	s.MCPServer.AddTool(listPkgsTool, handleListPkgs)

	// read_env: Facilitates context gathering from the current Termux session.
	readEnvTool := mcp.NewTool("read_env",
		mcp.WithDescription("Read environment variables"),
		mcp.WithString("name", mcp.Description("Name of the environment variable. If empty, lists all")),
	)
	s.MCPServer.AddTool(readEnvTool, handleReadEnv)
}

func handleRunCommand(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	command, err := req.RequireString("command")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// args is now a string array so callers can pass arguments containing spaces
	// or shell metacharacters without ambiguous splitting.
	args := req.GetStringSlice("args", nil)

	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error: %v\nOutput: %s", err, string(output))), nil
	}

	return mcp.NewToolResultText(string(output)), nil
}

func handleInstallPkg(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pkgName, err := req.RequireString("package")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Use -y flag to ensure non-interactive installation.
	cmd := exec.CommandContext(ctx, "pkg", "install", "-y", pkgName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error: %v\nOutput: %s", err, string(output))), nil
	}

	return mcp.NewToolResultText(string(output)), nil
}

func handleListPkgs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cmd := exec.CommandContext(ctx, "pkg", "list-installed")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(output)), nil
}

func handleReadEnv(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	if name != "" {
		val := os.Getenv(name)
		return mcp.NewToolResultText(fmt.Sprintf("%s=%s", name, val)), nil
	}

	var result strings.Builder
	for _, e := range os.Environ() {
		result.WriteString(e + "\n")
	}
	return mcp.NewToolResultText(result.String()), nil
}
