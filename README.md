# DroidMCP

> 🇪🇸 [Versión en español](README.es.md)

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
    +---------+---------+----------+---------+-----------+
    |         |         |          |         |           |
 filesystem github   scraper   termux   network   clipboard
```

## Servers

### mcp-filesystem

Secure file operations within a configurable root directory. Includes path traversal protection.

| Tool | Description |
|------|-------------|
| `read_file` | Read the contents of a file |
| `read_file_lines` | Read a line range from a file |
| `write_file` | Write or create a file (creates parent dirs) |
| `list_directory` | List directory contents with type and size |
| `stat` | File metadata: size, mode, times, owner |
| `search_files` | Recursive file search using glob patterns |
| `delete_file` | Delete a file or empty directory |
| `move_file` | Move or rename a file/directory |
| `copy_file` | Copy a file |

### mcp-github

Full GitHub operations using a Personal Access Token. Built on `google/go-github`.

| Tool | Description |
|------|-------------|
| `list_repos` | List repositories for the authenticated user |
| `get_repo` | Get detailed repository metadata |
| `list_branches` / `list_tags` / `list_releases` | List repository refs and releases |
| `list_commits` / `get_commit` | Browse commit history and details |
| `fork_repo` | Fork a repository |
| `create_issue` | Open a new issue |
| `list_issues` | List issues (filterable by state) |
| `comment_issue` / `close_issue` / `label_issue` | Manage existing issues |
| `get_file` | Read a file from a repository (auto-decodes Base64) |
| `get_pr` | Get pull request details |
| `create_pr` | Create a new pull request |
| `review_pr` / `merge_pr` | Review and merge pull requests |
| `commit_file` | Create or update a file via the Content API |
| `search_code` / `search_issues` | Search code and issues across GitHub |

### mcp-scraper

Lightweight web scraping without Chromium or Playwright. Built on `colly` and `goquery`.

| Tool | Description |
|------|-------------|
| `fetch_page` | Fetch raw HTML from a URL |
| `extract_text` | Extract clean text (strips scripts, styles, noise) |
| `extract_links` | Extract all absolute URLs from a page |
| `extract_table` | Extract HTML tables as structured JSON |
| `extract_metadata` | Extract title, description, canonical, `og:*`, `twitter:*` |
| `search_in_page` | Search text or a regex in a page's visible text, with context |

### mcp-termux

Direct interaction with the Termux environment. Enables AI agents to execute commands and manage packages.

| Tool | Description |
|------|-------------|
| `run_command` | Execute a shell command |
| `install_pkg` | Install a package via `pkg install` |
| `list_pkgs` | List installed packages |
| `read_env` | Read one or all environment variables |
| `get_storage` | Storage usage for home, prefix, and shared storage |
| `termux_battery_status` / `termux_location` | Device status via Termux:API |
| `termux_notification` / `termux_toast` | Show notifications and toasts |
| `termux_sms_send` / `termux_tts_speak` | Send SMS and speak text via TTS |

### mcp-network

Local network discovery and port scanning using concurrent TCP probes.

| Tool | Description |
|------|-------------|
| `scan_network` | Scan a subnet for active hosts (auto-detects local subnet) |
| `check_ports` | Scan common ports on a specific host |
| `nslookup` / `reverse_dns` | Forward and reverse DNS lookups |
| `traceroute` | Trace the path to a host (no root needed via `tracepath`) |
| `network_info` | Gateway, DNS servers, interfaces, detected subnet |
| `list_devices` | List devices remembered from previous scans (persistent inventory) |
| `get_device_info` | Details for one known device by IP or MAC |

### mcp-clipboard

Clipboard management between Android and AI agents via Termux API.

> Requires the `termux-api` package (`pkg install termux-api`) **and** the
> [Termux:API](https://wiki.termux.com/wiki/Termux:API) Android app.
> Without them the tools fail with a hint explaining which step is missing.

| Tool | Description |
|------|-------------|
| `get_clipboard` | Read current clipboard content (supports binary via base64) |
| `set_clipboard` | Write text or base64-encoded bytes to clipboard |
| `clear_clipboard` | Reset the clipboard to an empty value |
| `clipboard_history` | Retrieve in-memory clipboard history (FIFO-evicted, bounded by env vars) |

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
  droidmcp-clipboard
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
The full operational guide (auth, TLS, logging, threat model, production
checklist) lives in [`docs/security.md`](docs/security.md). The table
below is a quick reference.

### Core (every server)

| Variable | Description | Default |
|----------|-------------|---------|
| `DROIDMCP_PORT` | TCP port the SSE listener binds to | `3000` |
| `DROIDMCP_ROOT` | Root for filesystem ops; validated at startup. **Required by `mcp-filesystem`** (it refuses to start without it). | `/` (used only by other servers, which ignore it) |
| `DROIDMCP_API_KEY` | Global API key. If set, every request must carry it in `X-DroidMCP-Key` | unset (dev mode) |
| `DROIDMCP_<SERVER>_KEY` | Per-server override, e.g. `DROIDMCP_TERMUX_KEY`. Wins over the global key. | unset |
| `DROIDMCP_TLS_CERT` | Path to TLS certificate (PEM). If set together with `_KEY`, enables HTTPS + HSTS. | unset |
| `DROIDMCP_TLS_KEY` | Path to TLS private key (PEM). | unset |
| `DROIDMCP_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `DROIDMCP_LOG_FORMAT` | `json` for structured logs, anything else for text | `text` |

### Per-server

| Variable | Used by | Description |
|----------|---------|-------------|
| `GITHUB_TOKEN` / `GITHUB_APP_TOKEN` / `GITHUB_FINE_GRAINED_TOKEN` | `mcp-github` | Required. First one set is used. |
| `DROIDMCP_MAX_READ_BYTES` | `mcp-filesystem` | Cap on bytes a single `read_file` buffers in memory (default 10 MiB). Page larger files with `offset`/`length`. |
| `DROIDMCP_TERMUX_ALLOWLIST` | `mcp-termux` | Comma-separated allowlist for `run_command` (empty = allow all). |
| `DROIDMCP_SCRAPER_ALLOW_PRIVATE` | `mcp-scraper` | Set to `1` to allow RFC1918/loopback URLs (off by default for SSRF safety). |
| `DROIDMCP_NETWORK_ALLOW_PUBLIC` | `mcp-network` | Set to `1` to allow non-RFC1918 scan targets. |
| `DROIDMCP_NETWORK_DB` | `mcp-network` | Path to the persistent device inventory JSON (default `~/.droidmcp/network-devices.json`). |
| `DROIDMCP_CLIPBOARD_HISTORY_ENTRIES` | `mcp-clipboard` | Cap on in-memory history entries. |
| `DROIDMCP_CLIPBOARD_HISTORY_BYTES` | `mcp-clipboard` | Cap on in-memory history bytes. |

### Health and auth

- `GET /healthz` always returns `200 {"status":"ok","server":<name>,"version":<v>}` and bypasses auth so a supervisor (systemd, Docker, k8s) can probe the server without holding the key.
- Every other route requires the `X-DroidMCP-Key` header when `DROIDMCP_API_KEY` (or the per-server override) is set. Comparison is constant-time. With no key configured, most servers log `auth=disabled` and accept every request — use this only on `localhost`. `mcp-termux` and `mcp-filesystem` are exceptions: they refuse to start without a key.

---

## Usage

Each server starts an HTTP/SSE endpoint. The SSE stream is available at `http://localhost:<port>/sse`.

### Filesystem

```bash
export DROIDMCP_PORT=3000
export DROIDMCP_ROOT=/sdcard/Documents          # required — server won't start without it
export DROIDMCP_FILESYSTEM_KEY="$(openssl rand -base64 32)"  # required — or DROIDMCP_API_KEY
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

### Clipboard

```bash
export DROIDMCP_PORT=3005
droidmcp-clipboard
```

### Production example (auth + TLS)

```bash
# One strong shared key (or per-server keys with DROIDMCP_<NAME>_KEY).
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"

# TLS material — required for any non-loopback exposure.
export DROIDMCP_TLS_CERT=/etc/droidmcp/cert.pem
export DROIDMCP_TLS_KEY=/etc/droidmcp/key.pem

# Lock filesystem to a dedicated directory; never leave it at "/".
export DROIDMCP_ROOT=/srv/droidmcp/workspace

# JSON logs for shipping; info level is plenty in steady state.
export DROIDMCP_LOG_FORMAT=json
export DROIDMCP_LOG_LEVEL=info

droidmcp-filesystem
```

Health probes from a supervisor do not need the key:

```bash
curl -fsS https://localhost:3000/healthz
# {"status":"ok","server":"mcp-filesystem","version":"dev"}
# (version is "dev" for local builds; release binaries report the git tag)
```

Clients pass the key in `X-DroidMCP-Key`:

```bash
curl -H "X-DroidMCP-Key: $DROIDMCP_API_KEY" https://localhost:3000/sse
```

---

## Client Integration

### Claude Code

Add servers to your Claude Code MCP config (`~/.claude/settings.json`).
When `DROIDMCP_API_KEY` (or a per-server key) is set, include the
`X-DroidMCP-Key` header in the entry:

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "sse",
      "url": "http://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<paste-the-key>" }
    },
    "github": {
      "type": "sse",
      "url": "http://localhost:3001/sse",
      "headers": { "X-DroidMCP-Key": "<paste-the-key>" }
    }
  }
}
```

For TLS, switch the URL to `https://…` after configuring
`DROIDMCP_TLS_CERT` / `DROIDMCP_TLS_KEY` on the server.

### Gemini CLI

Add the SSE endpoint to your Gemini CLI configuration, with the same
`X-DroidMCP-Key` header when auth is enabled:

```json
{
  "mcpServers": {
    "filesystem": {
      "uri": "http://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<paste-the-key>" }
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
│   ├── network/          # Network scanning MCP
│   └── clipboard/        # Clipboard management MCP
├── internal/
│   ├── core/server.go    # Shared MCP server wrapper (HTTP/SSE)
│   ├── logger/logger.go  # Structured logging (stderr)
│   └── config/config.go  # Environment-based configuration
├── scripts/
│   └── build-arm64.sh    # Cross-compilation script
├── docs/
│   ├── setup-termux.md   # Detailed Termux setup guide
│   └── security.md       # Threat model + dev/prod operations guide
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

Read [`docs/security.md`](docs/security.md) for the full threat model and the
production checklist. Highlights:

- **`mcp-filesystem` requires an explicit `DROIDMCP_ROOT` and an API key.** It
  refuses to start without both, so an unconfigured server can never fall back
  to the insecure `/` default or run unauthenticated.
- **Dev mode vs production.** With no `DROIDMCP_API_KEY` (and no per-server
  key), most servers accept every request and the startup banner logs
  `auth=disabled`. That mode is meant for a single shell on `localhost`.
  Anywhere else, set a random key and enable TLS via `DROIDMCP_TLS_CERT` /
  `DROIDMCP_TLS_KEY`. (`mcp-termux` and `mcp-filesystem` have no dev mode.)
- **`mcp-termux` is a remote shell.** Restrict it with `DROIDMCP_TERMUX_ALLOWLIST`,
  give it a dedicated `DROIDMCP_TERMUX_KEY`, and do not start it at all if you do
  not need it.
- **`mcp-clipboard` needs `termux-api`.** Install the Android app and run
  `pkg install termux-api` in Termux; otherwise tools return a clear hint and
  fail. See [`docs/setup-termux.md`](docs/setup-termux.md).
- **`mcp-filesystem`** rejects absolute paths and `..` traversal, then resolves
  symlinks and re-checks containment so a symlink under the root cannot point
  outside it (audit item 2.2 closed). The check isn't fully TOCTOU-proof, so
  still avoid roots that other untrusted processes can write to.
- **`mcp-scraper` / `mcp-network`** ship with safe defaults (no RFC1918 / no
  public targets respectively). Override only when you understand the SSRF /
  network-scan implications.
- **`mcp-github`** acts within whatever the provided token allows. Use the
  smallest scope possible.
- **Logs are redacted.** Attribute keys matching `token`, `secret`, `password`,
  `api_key`, `authorization`, or `key` (as a word) are replaced with
  `[REDACTED]` before reaching the sink. The `X-DroidMCP-Key` header is never
  logged.
- **Releases are reproducible and signed.** Each release ships a `SHA256SUMS`
  file plus cosign `.sig` and `.pem`. Verify before installing.

---

## Contributing

Contributions are welcome. Read the guide in
[CONTRIBUTING.md](CONTRIBUTING.md) and see [ROADMAP.md](ROADMAP.md) for
planned features and open phases.

1. Fork the repository
2. Create a feature branch
3. Submit a pull request

---

## License

MIT - see [LICENSE](LICENSE) for details.

Made from Android, for Android.

---

Developed with love by Ale!
