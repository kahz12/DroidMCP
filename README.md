# DroidMCP

Native MCP (Model Context Protocol) servers for Android/Termux. High-performance ARM64 binaries written in Go with zero external runtime dependencies.

No Node.js. No Python. Just a single binary that works.

---

## Overview

DroidMCP is a monorepo of MCP servers designed to run natively on Android through Termux. Each server exposes a set of tools over HTTP/SSE that any MCP-compatible client (Claude Code, Gemini CLI, etc.) can consume directly.

```
Claude Code / Gemini CLI / Any MCP Client
              |
              | HTTP/SSE (MCP Protocol)
              v
       DroidMCP Server        <-- runs in Termux (Android)
              |
    +---------+---------+----------+---------+
    |         |         |          |         |
 filesystem github   scraper   termux   network
```

## Servers

### mcp-filesystem

Secure file operations within a configurable root directory. Includes path traversal protection.

| Tool | Description |
|------|-------------|
| `read_file` | Read the contents of a file |
| `write_file` | Write or create a file (creates parent dirs) |
| `list_directory` | List directory contents with type and size |
| `search_files` | Recursive file search using glob patterns |
| `delete_file` | Delete a file or empty directory |
| `move_file` | Move or rename a file/directory |

### mcp-github

Full GitHub operations using a Personal Access Token. Built on `google/go-github`.

| Tool | Description |
|------|-------------|
| `list_repos` | List repositories for the authenticated user |
| `get_repo` | Get detailed repository metadata |
| `create_issue` | Open a new issue |
| `list_issues` | List issues (filterable by state) |
| `get_file` | Read a file from a repository (auto-decodes Base64) |
| `get_pr` | Get pull request details |
| `create_pr` | Create a new pull request |
| `commit_file` | Create or update a file via the Content API |

### mcp-scraper

Lightweight web scraping without Chromium or Playwright. Built on `colly` and `goquery`.

| Tool | Description |
|------|-------------|
| `fetch_page` | Fetch raw HTML from a URL |
| `extract_text` | Extract clean text (strips scripts, styles, noise) |
| `extract_links` | Extract all absolute URLs from a page |
| `extract_table` | Extract HTML tables as structured JSON |

### mcp-termux

Direct interaction with the Termux environment. Enables AI agents to execute commands and manage packages.

| Tool | Description |
|------|-------------|
| `run_command` | Execute a shell command |
| `install_pkg` | Install a package via `pkg install` |
| `list_pkgs` | List installed packages |
| `read_env` | Read one or all environment variables |

### mcp-network

Local network discovery and port scanning using concurrent TCP probes.

| Tool | Description |
|------|-------------|
| `scan_network` | Scan a subnet for active hosts (auto-detects local subnet) |
| `check_ports` | Scan common ports on a specific host |

---

## Installation

### Prerequisites

- Android device with [Termux](https://f-droid.org/en/packages/com.termux/) installed (F-Droid recommended)
- Go, Git, and Make available in Termux

```bash
pkg update && pkg upgrade
pkg install golang git make
```

### Build from source

```bash
git clone https://github.com/kahz12/DroidMCP
cd DroidMCP
make build
```

Binaries are output to `bin/`:

```
bin/
  droidmcp-filesystem
  droidmcp-github
  droidmcp-scraper
  droidmcp-termux
  droidmcp-network
```

### Install to PATH (optional)

```bash
make install
```

This copies all binaries to Termux's `$PREFIX/bin`, making them available globally.

### Cross-compile for ARM64

If building from a different machine:

```bash
make build-arm64
```

---

## Configuration

All servers are configured via environment variables prefixed with `DROIDMCP_`.

| Variable | Description | Default |
|----------|-------------|---------|
| `DROIDMCP_PORT` | HTTP port for the MCP server | `3000` |
| `DROIDMCP_ROOT` | Root directory for filesystem operations | `/` |
| `GITHUB_TOKEN` | Personal Access Token (required for mcp-github) | - |

---

## Usage

Each server starts an HTTP/SSE endpoint. The SSE stream is available at `http://localhost:<port>/sse`.

### Filesystem

```bash
export DROIDMCP_PORT=3000
export DROIDMCP_ROOT=/sdcard/Documents
droidmcp-filesystem
```

### GitHub

```bash
export DROIDMCP_PORT=3001
export GITHUB_TOKEN=ghp_your_token_here
droidmcp-github
```

### Scraper

```bash
export DROIDMCP_PORT=3002
droidmcp-scraper
```

### Termux

```bash
export DROIDMCP_PORT=3003
droidmcp-termux
```

### Network

```bash
export DROIDMCP_PORT=3004
droidmcp-network
```

---

## Client Integration

### Claude Code

Add servers to your Claude Code MCP config (`~/.claude/settings.json`):

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "sse",
      "url": "http://localhost:3000/sse"
    },
    "github": {
      "type": "sse",
      "url": "http://localhost:3001/sse"
    }
  }
}
```

### Gemini CLI

Add the SSE endpoint in your Gemini CLI configuration:

```json
{
  "mcpServers": {
    "filesystem": {
      "uri": "http://localhost:3000/sse"
    }
  }
}
```

---

## Project Structure

```
DroidMCP/
├── cmd/
│   ├── filesystem/       # File operations MCP
│   ├── github/           # GitHub API MCP
│   ├── scraper/          # Web scraping MCP
│   ├── termux/           # Shell & package management MCP
│   └── network/          # Network scanning MCP
├── internal/
│   ├── core/server.go    # Shared MCP server wrapper (HTTP/SSE)
│   ├── logger/logger.go  # Structured logging (stderr)
│   └── config/config.go  # Environment-based configuration
├── scripts/
│   └── build-arm64.sh    # Cross-compilation script
├── docs/
│   └── setup-termux.md   # Detailed Termux setup guide
├── .github/workflows/
│   └── build.yml         # CI/CD: build + release on tag
├── Makefile
├── go.mod
└── go.sum
```

## Tech Stack

| Component | Technology |
|-----------|------------|
| Language | Go |
| MCP Transport | HTTP/SSE |
| MCP SDK | [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) |
| GitHub Client | [google/go-github](https://github.com/google/go-github) |
| Web Scraping | [gocolly/colly](https://github.com/gocolly/colly) + [goquery](https://github.com/PuerkitoBio/goquery) |
| Configuration | [spf13/viper](https://github.com/spf13/viper) |
| Build Target | `GOOS=linux GOARCH=arm64` |

---

## Security Considerations

- **Filesystem**: All paths are validated against a configurable root. Absolute paths and directory traversal attempts (`../`) are rejected.
- **Termux**: Provides full shell access. Use with caution and only with trusted MCP clients.
- **Network**: Runs on localhost only. Never expose server ports to external networks.
- **GitHub**: Permissions are scoped to whatever the provided `GITHUB_TOKEN` allows.

---

## Contributing

Contributions are welcome. See [ROADMAP.md](ROADMAP.md) for planned features and open phases.

1. Fork the repository
2. Create a feature branch
3. Submit a pull request

---

## License

MIT - see [LICENSE](LICENSE) for details.

Made from Android, for Android.

---

Developed with love by Ale!
