package main

import "github.com/mark3labs/mcp-go/mcp"

// runCommandTool exposes generic shell access, subject to DROIDMCP_TERMUX_ALLOWLIST.
func runCommandTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("run_command",
			mcp.WithDescription("Execute a command in Termux shell. Returns JSON {stdout, stderr, exit_code, ...}."),
			mcp.WithString("command", mcp.Required(), mcp.Description("The program to execute (no shell)")),
			mcp.WithArray("args", mcp.WithStringItems(),
				mcp.Description("Arguments, one per element (preserves spaces and metacharacters in each arg)")),
			mcp.WithString("cwd", mcp.Description("Working directory for the child process")),
			mcp.WithObject("env_extra", mcp.Description("Extra environment variables to set on top of the parent env")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 30s, max 300s.")),
		),
		Handler: handleRunCommand,
	}
}

func installPkgTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("install_pkg",
			mcp.WithDescription("Install a package via pkg install -y"),
			mcp.WithString("package", mcp.Required(), mcp.Description("Package name")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 30s, max 300s.")),
		),
		Handler: handleInstallPkg,
	}
}

func listPkgsTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("list_pkgs",
			mcp.WithDescription("List installed packages"),
		),
		Handler: handleListPkgs,
	}
}

func readEnvTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("read_env",
			mcp.WithDescription("Read environment variables. Returns JSON {name, value} or {vars: {...}} when no name is given."),
			mcp.WithString("name", mcp.Description("Name of the environment variable. If empty, lists all")),
		),
		Handler: handleReadEnv,
	}
}

// shellTools returns the generic shell, package, and environment tools.
func shellTools() []ToolSpec {
	return []ToolSpec{
		runCommandTool(),
		runShellTool(),
		installPkgTool(),
		listPkgsTool(),
		readEnvTool(),
	}
}
