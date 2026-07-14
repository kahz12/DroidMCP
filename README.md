<div align="center">

# DroidMCP

**Native Model Context Protocol servers for Android and Termux.**

Single-binary MCP servers written in Go. ARM64-native, zero runtime dependencies — no Node.js, no Python, no interpreter to install.

[![Build and Release](https://github.com/kahz12/DroidMCP/actions/workflows/build.yml/badge.svg)](https://github.com/kahz12/DroidMCP/actions/workflows/build.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg?logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/platform-Android%20%C2%B7%20Termux-3DDC84.svg?logo=android&logoColor=white)](https://termux.dev)
[![Arch](https://img.shields.io/badge/arch-ARM64-555.svg)](scripts/build-arm64.sh)

[English](README.md) · [Español](README.es.md) · [Usage](docs/usage.md) · [Roadmap](ROADMAP.md) · [Security](docs/security.md)

</div>

---

## Overview

DroidMCP is a monorepo of MCP servers built to run natively on Android through Termux. Each server exposes a focused set of tools over HTTP/SSE that any MCP-compatible client — Claude Code, Gemini CLI, or your own — can consume directly.

```
   Claude Code / Gemini CLI / Any MCP Client
                     │
                     │  HTTP/SSE (MCP Protocol)
                     ▼
              DroidMCP Server          runs in Termux (Android)
                     │
   ┌────────────┬────────────┬──────────┬────────────┬────────────┐
   ▼            ▼            ▼          ▼            ▼            ▼
filesystem   github      scraper    termux      network     clipboard
```

## Servers

| Server | Focus | Default port |
|--------|-------|:---:|
| `mcp-filesystem` | Sandboxed file operations with path-traversal protection | `3000` |
| `mcp-github` | Full GitHub API access via Personal Access Token | `3001` |
| `mcp-scraper` | Chromium-free web scraping and extraction | `3002` |
| `mcp-termux` | Shell execution and package management | `3003` |
| `mcp-network` | LAN discovery and port scanning | `3004` |
| `mcp-clipboard` | Android clipboard bridge via Termux:API | `3005` |

<details open>
<summary><b>mcp-filesystem</b> — secure file operations within a configurable root</summary>

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

</details>

<details>
<summary><b>mcp-github</b> — full GitHub operations, built on <code>google/go-github</code></summary>

| Tool | Description |
|------|-------------|
| `list_repos` | List repositories for the authenticated user |
| `get_repo` | Get detailed repository metadata |
| `list_branches` · `list_tags` · `list_releases` | List repository refs and releases |
| `list_commits` · `get_commit` | Browse commit history and details |
| `fork_repo` | Fork a repository |
| `create_issue` · `list_issues` | Open and list issues (filterable by state) |
| `comment_issue` · `close_issue` · `label_issue` | Manage existing issues |
| `get_file` | Read a file from a repository (auto-decodes Base64) |
| `get_pr` · `create_pr` | Read and open pull requests |
| `review_pr` · `merge_pr` | Review and merge pull requests |
| `commit_file` | Create or update a file via the Content API |
| `search_code` · `search_issues` | Search code and issues across GitHub |

</details>

<details>
<summary><b>mcp-scraper</b> — lightweight scraping on <code>colly</code> + <code>goquery</code>, no Chromium</summary>

| Tool | Description |
|------|-------------|
| `fetch_page` | Fetch raw HTML from a URL |
| `extract_text` | Extract clean text (strips scripts, styles, noise) |
| `extract_links` | Extract all absolute URLs from a page |
| `extract_table` | Extract HTML tables as structured JSON |
| `extract_metadata` | Extract title, description, canonical, `og:*`, `twitter:*` |
| `search_in_page` | Search text or regex in visible text, with context |

</details>

<details>
<summary><b>mcp-termux</b> — direct interaction with the Termux environment</summary>

| Tool | Description |
|------|-------------|
| `run_command` | Execute a shell command |
| `install_pkg` | Install a package via `pkg install` |
| `list_pkgs` | List installed packages |
| `read_env` | Read one or all environment variables |
| `get_storage` | Storage usage for home, prefix, and shared storage |
| `termux_battery_status` · `termux_location` | Device status via Termux:API |
| `termux_notification` · `termux_toast` | Show notifications and toasts |
| `termux_sms_send` · `termux_tts_speak` | Send SMS and speak text via TTS |

</details>

<details>
<summary><b>mcp-network</b> — LAN discovery via concurrent TCP probes</summary>

| Tool | Description |
|------|-------------|
| `scan_network` | Scan a subnet for active hosts (auto-detects local subnet) |
| `check_ports` | Scan common ports on a specific host |
| `nslookup` · `reverse_dns` | Forward and reverse DNS lookups |
| `traceroute` | Trace the path to a host (no root, via `tracepath`) |
| `network_info` | Gateway, DNS servers, interfaces, detected subnet |
| `list_devices` | List devices from previous scans (persistent inventory) |
| `get_device_info` | Details for one known device by IP or MAC |

</details>

<details>
<summary><b>mcp-clipboard</b> — Android clipboard bridge (requires Termux:API)</summary>

> Requires the `termux-api` package (`pkg install termux-api`) **and** the
> [Termux:API](https://wiki.termux.com/wiki/Termux:API) Android app. Without them
> the tools fail with a hint explaining which step is missing.

| Tool | Description |
|------|-------------|
| `get_clipboard` | Read current clipboard content (binary via base64) |
| `set_clipboard` | Write text or base64-encoded bytes to clipboard |
| `clear_clipboard` | Reset the clipboard to an empty value |
| `clipboard_history` | In-memory clipboard history (FIFO-evicted, env-bounded) |

</details>

---

## Installation

**Prerequisites** — an Android device with [Termux](https://f-droid.org/en/packages/com.termux/) (F-Droid build recommended), plus Go, Git, and Make:

```bash
pkg update && pkg upgrade
pkg install golang git make
```

**Build from source:**

```bash
git clone https://github.com/kahz12/DroidMCP
cd DroidMCP
make build            # binaries land in bin/
make install          # optional: copy to $PREFIX/bin (global)
make build-arm64      # cross-compile from another machine
```

`make build` produces one binary per server in `bin/`: `droidmcp-filesystem`,
`droidmcp-github`, `droidmcp-scraper`, `droidmcp-termux`, `droidmcp-network`,
`droidmcp-clipboard`.

---

## Configuration

All servers read environment variables prefixed with `DROIDMCP_`. The table below
is a quick reference; the full operational guide (auth, TLS, logging, threat model)
lives in [`docs/security.md`](docs/security.md).

**Core — every server:**

| Variable | Description | Default |
|----------|-------------|---------|
| `DROIDMCP_PORT` | TCP port the SSE listener binds to | `3000` |
| `DROIDMCP_ROOT` | Filesystem root, validated at startup. **Required by `mcp-filesystem`.** | `/` (ignored by other servers) |
| `DROIDMCP_API_KEY` | Global key. If set, every request must carry `X-DroidMCP-Key` | unset (dev mode) |
| `DROIDMCP_<SERVER>_KEY` | Per-server override, e.g. `DROIDMCP_TERMUX_KEY`; wins over the global key | unset |
| `DROIDMCP_TLS_CERT` · `DROIDMCP_TLS_KEY` | PEM cert and key. Both set enables HTTPS + HSTS | unset |
| `DROIDMCP_LOG_LEVEL` | `debug` · `info` · `warn` · `error` | `info` |
| `DROIDMCP_LOG_FORMAT` | `json` for structured logs, otherwise text | `text` |

**Per-server:**

| Variable | Used by | Description |
|----------|---------|-------------|
| `GITHUB_TOKEN` · `GITHUB_APP_TOKEN` · `GITHUB_FINE_GRAINED_TOKEN` | `mcp-github` | Required; first one set is used |
| `DROIDMCP_MAX_READ_BYTES` | `mcp-filesystem` | Per-read buffer cap (default 10 MiB); page larger files with `offset`/`length` |
| `DROIDMCP_TERMUX_ALLOWLIST` | `mcp-termux` | Comma-separated `run_command` allowlist (empty = allow all) |
| `DROIDMCP_SCRAPER_ALLOW_PRIVATE` | `mcp-scraper` | `1` allows RFC1918/loopback URLs (off by default, SSRF safety) |
| `DROIDMCP_NETWORK_ALLOW_PUBLIC` | `mcp-network` | `1` allows non-RFC1918 scan targets |
| `DROIDMCP_NETWORK_DB` | `mcp-network` | Persistent device inventory path (default `~/.droidmcp/network-devices.json`) |
| `DROIDMCP_CLIPBOARD_HISTORY_ENTRIES` · `_BYTES` | `mcp-clipboard` | In-memory history caps |

**Health and auth** — `GET /healthz` always returns `200` and bypasses auth so a
supervisor (systemd, Docker, k8s) can probe without the key. Every other route
requires `X-DroidMCP-Key` when a key is configured; comparison is constant-time.
With no key, most servers log `auth=disabled` and accept every request — use only
on `localhost`. `mcp-termux` and `mcp-filesystem` are exceptions: they refuse to
start without a key.

---

## Usage

Each server binds an HTTP/SSE listener; the stream is served at
`http://localhost:<port>/sse`. Set `DROIDMCP_PORT`, export any required variables,
and run the binary:

| Server | Port | Command | Required environment |
|--------|:---:|---------|----------------------|
| filesystem | `3000` | `droidmcp-filesystem` | `DROIDMCP_ROOT` + a key |
| github | `3001` | `droidmcp-github` | `GITHUB_TOKEN` |
| scraper | `3002` | `droidmcp-scraper` | — |
| termux | `3003` | `droidmcp-termux` | a key |
| network | `3004` | `droidmcp-network` | — |
| clipboard | `3005` | `droidmcp-clipboard` | `termux-api` package + app |

**Production example — filesystem with auth and TLS:**

```bash
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"   # or per-server DROIDMCP_<NAME>_KEY
export DROIDMCP_TLS_CERT=/etc/droidmcp/cert.pem        # required for non-loopback exposure
export DROIDMCP_TLS_KEY=/etc/droidmcp/key.pem
export DROIDMCP_ROOT=/srv/droidmcp/workspace           # never leave this at "/"
export DROIDMCP_LOG_FORMAT=json                        # structured logs for shipping

droidmcp-filesystem
```

Supervisors probe health without the key; clients pass the key in the header:

```bash
curl -fsS https://localhost:3000/healthz
# {"status":"ok","server":"mcp-filesystem","version":"dev"}

curl -H "X-DroidMCP-Key: $DROIDMCP_API_KEY" https://localhost:3000/sse
```

> `version` is `dev` for local builds; release binaries report the git tag.

---

## Client Integration

**Claude Code** — add the servers to `~/.claude/settings.json`, including the
`X-DroidMCP-Key` header when a key is set:

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

**Gemini CLI** — same endpoint and header, `uri` instead of `url`:

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

Switch URLs to `https://…` once `DROIDMCP_TLS_CERT` / `DROIDMCP_TLS_KEY` are set.

---

## Project Structure

```
DroidMCP/
├── cmd/                    # one main package per server
│   ├── filesystem/  github/  scraper/
│   └── termux/  network/  clipboard/
├── internal/
│   ├── core/server.go      # shared MCP server wrapper (HTTP/SSE)
│   ├── logger/logger.go    # structured logging (stderr)
│   └── config/config.go    # environment-based configuration
├── scripts/build-arm64.sh  # cross-compilation
├── docs/                   # setup-termux.md · security.md
├── .github/workflows/      # build.yml — CI: build + release on tag
└── Makefile · go.mod · go.sum
```

## Tech Stack

| Component | Technology |
|-----------|------------|
| Language | Go 1.26 |
| MCP transport | HTTP/SSE |
| MCP SDK | [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) |
| GitHub client | [google/go-github](https://github.com/google/go-github) |
| Web scraping | [gocolly/colly](https://github.com/gocolly/colly) + [goquery](https://github.com/PuerkitoBio/goquery) |
| Configuration | [spf13/viper](https://github.com/spf13/viper) |
| Build target | `GOOS=linux GOARCH=arm64` |

---

## Security

Full threat model and production checklist in [`docs/security.md`](docs/security.md). Highlights:

- **`mcp-filesystem` and `mcp-termux` have no dev mode.** Both refuse to start
  without an explicit key, and filesystem also requires `DROIDMCP_ROOT` — an
  unconfigured server can never fall back to `/` or run unauthenticated.
- **Dev vs. production.** Other servers accept every request when no key is set
  (banner logs `auth=disabled`) — meant only for a single shell on `localhost`.
  Anywhere else, set a random key and enable TLS.
- **`mcp-termux` is a remote shell.** Restrict it with `DROIDMCP_TERMUX_ALLOWLIST`,
  give it a dedicated key, and don't run it unless you need it.
- **Safe network defaults.** `mcp-scraper` blocks RFC1918/loopback and `mcp-network`
  blocks public targets; override only when you understand the SSRF / scanning implications.
- **Path safety.** `mcp-filesystem` rejects absolute paths and `..`, resolves
  symlinks, and re-checks containment. Not fully TOCTOU-proof — avoid roots that
  other untrusted processes can write to.
- **Logs are redacted.** Attribute keys matching `token`, `secret`, `password`,
  `api_key`, `authorization`, or `key` are replaced with `[REDACTED]`; the
  `X-DroidMCP-Key` header is never logged.
- **Signed releases.** Each release ships `SHA256SUMS` plus cosign `.sig` and
  `.pem`. Verify before installing.

---

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) and check
[ROADMAP.md](ROADMAP.md) for planned work. Fork, branch, and open a pull request.

## License

Released under the [MIT License](LICENSE).

<div align="center">

Built on Android, for Android — by Ale.

</div>
