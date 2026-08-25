<div align="center">

# DroidMCP

**Native Model Context Protocol servers for Android and Termux**

Self-contained Go binaries that give any MCP client — Claude Code, Gemini CLI, or your own —
hands on an Android device. No Node.js, no Python, no runtime to install.

[![CI](https://img.shields.io/github/actions/workflow/status/kahz12/DroidMCP/build.yml?branch=main&style=flat-square&label=CI)](https://github.com/kahz12/DroidMCP/actions/workflows/build.yml)
[![Release](https://img.shields.io/github/v/release/kahz12/DroidMCP?style=flat-square)](https://github.com/kahz12/DroidMCP/releases/latest)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/Android%20%C2%B7%20Termux-ARM64-3DDC84?style=flat-square&logo=android&logoColor=white)](https://termux.dev)
[![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)](LICENSE)

[English](README.md) · [Español](README.es.md) · [Usage Guide](docs/usage.md) · [Security](docs/security.md) · [Roadmap](ROADMAP.md)

</div>

---

## At a glance

| Zero dependencies | Secure by default | Thirteen focused servers |
|:---|:---|:---|
| One static ARM64 binary per server, pure Go — no CGO, no interpreter, nothing else to install. | Loopback-only listener, API-key auth, optional TLS, sandboxed roots, redacted logs, signed releases. | Files, GitHub, web scraping, shell, LAN, clipboard, media, SQLite, device sensors, notifications, contacts, SMS, and on-device LLMs — each behind a small, auditable tool surface. |

```
      Claude Code · Gemini CLI · any MCP client
                          │
                          │  MCP protocol over HTTP/SSE
                          ▼
       ┌──────────────────────────────────────┐
       │      DroidMCP — Termux · ARM64       │
       ├────────────┬────────────┬────────────┤
       │ filesystem │   github   │  scraper   │
       ├────────────┼────────────┼────────────┤
       │   termux   │  network   │ clipboard  │
       ├────────────┼────────────┼────────────┤
       │   media    │   sqlite   │  sensors   │
       ├────────────┴────────────┴────────────┤
       │            notifications             │
       ├────────────┬────────────┬────────────┤
       │  contacts  │    sms     │ llm-proxy  │
       └────────────┴────────────┴────────────┘
```

## Servers

| Server | Port | Focus | Requires |
|--------|:---:|-------|----------|
| `mcp-filesystem` | `3000` | Sandboxed file operations with path-traversal protection | `DROIDMCP_ROOT` + key |
| `mcp-github` | `3001` | Full GitHub API via Personal Access Token | `GITHUB_TOKEN` + key |
| `mcp-scraper` | `3002` | Chromium-free web scraping and extraction | — |
| `mcp-termux` | `3003` | Shell execution and package management | key |
| `mcp-network` | `3004` | LAN discovery and port scanning | — |
| `mcp-clipboard` | `3005` | Android clipboard bridge via Termux:API | `termux-api` |
| `mcp-media` | `3006` | Media browsing and `ffmpeg`-based transforms | `DROIDMCP_ROOT` + key |
| `mcp-sqlite` | `3007` | Local SQLite databases, pure Go — no CGO | `DROIDMCP_ROOT` + key |
| `mcp-sensors` | `3008` | Device sensors: battery, location, WiFi, brightness, volume | `termux-api` |
| `mcp-notifications` | `3009` | Android notifications and Do Not Disturb status | `termux-api` |
| `mcp-contacts` | `3010` | Read-only address book: search, export (JSON/vCard) | `termux-api` |
| `mcp-sms` | `3011` | Read SMS (OTP/2FA) and send real messages | `termux-api` + key |
| `mcp-llm-proxy` | `3012` | On-device LLMs through a local Ollama daemon | `ollama` |

Expand a server for its tool list; the full per-tool reference, with arguments and
examples, lives in the [usage guide](docs/usage.md).

<details>
<summary><b>mcp-filesystem</b> — secure file operations within a configurable root</summary>
<br>

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
<br>

| Tool | Description |
|------|-------------|
| `list_repos` · `get_repo` · `fork_repo` | Browse and fork repositories |
| `list_branches` · `list_tags` · `list_releases` | List repository refs and releases |
| `list_commits` · `get_commit` | Browse commit history and details |
| `create_issue` · `list_issues` | Open and list issues (filterable by state) |
| `comment_issue` · `close_issue` · `label_issue` | Manage existing issues |
| `get_file` · `commit_file` | Read and write repository files via the Content API |
| `get_pr` · `create_pr` · `review_pr` · `merge_pr` | Full pull-request lifecycle |
| `search_code` · `search_issues` | Search code and issues across GitHub |

</details>

<details>
<summary><b>mcp-scraper</b> — lightweight scraping on <code>colly</code> + <code>goquery</code>, no Chromium</summary>
<br>

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
<br>

| Tool | Description |
|------|-------------|
| `run_command` | Execute a shell command (allowlist-restrictable) |
| `install_pkg` · `list_pkgs` | Package management via `pkg` |
| `read_env` | Read one or all environment variables |
| `get_storage` | Storage usage for home, prefix, and shared storage |
| `termux_battery_status` · `termux_location` | Device status via Termux:API |
| `termux_notification` · `termux_toast` | Show notifications and toasts |
| `termux_sms_send` · `termux_tts_speak` | Send SMS and speak text via TTS |

</details>

<details>
<summary><b>mcp-network</b> — LAN discovery via concurrent TCP probes</summary>
<br>

| Tool | Description |
|------|-------------|
| `scan_network` | Scan a subnet for active hosts (auto-detects local subnet) |
| `check_ports` | Scan common ports on a specific host |
| `nslookup` · `reverse_dns` | Forward and reverse DNS lookups |
| `traceroute` | Trace the path to a host (no root, via `tracepath`) |
| `network_info` | Gateway, DNS servers, interfaces, detected subnet |
| `list_devices` · `get_device_info` | Persistent device inventory from previous scans |

</details>

<details>
<summary><b>mcp-clipboard</b> — Android clipboard bridge (requires Termux:API)</summary>
<br>

Requires the `termux-api` package and the [Termux:API](https://wiki.termux.com/wiki/Termux:API)
Android app; without them the tools fail with a hint explaining which step is missing.

| Tool | Description |
|------|-------------|
| `get_clipboard` | Read current clipboard content (binary via base64) |
| `set_clipboard` | Write text or base64-encoded bytes to clipboard |
| `clear_clipboard` | Reset the clipboard to an empty value |
| `clipboard_history` | In-memory clipboard history (FIFO-evicted, env-bounded) |

</details>

<details>
<summary><b>mcp-media</b> — media browsing and transforms within a configurable root</summary>
<br>

Listing and image dimensions are pure Go; conversion, thumbnails, and audio
extraction shell out to `ffmpeg`, and `get_metadata` is enriched by `exiftool`
when installed.

| Tool | Description |
|------|-------------|
| `list_media` | List image/video/audio files (recursive, filterable by kind) |
| `get_metadata` | Size, image dimensions, and EXIF/metadata for a file |
| `convert_image` | Convert image format and/or resize |
| `thumbnail` | Generate a thumbnail from an image or a video frame |
| `extract_audio` | Extract the audio track from a video |

</details>

<details>
<summary><b>mcp-sqlite</b> — local SQLite databases, pure Go (no CGO)</summary>
<br>

Backed by `modernc.org/sqlite`; databases are files under `DROIDMCP_ROOT`, and
every value is bound as a parameter — `?` placeholders keep statements
injection-safe.

| Tool | Description |
|------|-------------|
| `open_db` | Open a database, creating the file (and parent dirs) if needed |
| `query` | Run a read statement (SELECT/WITH/PRAGMA/…) and return rows as JSON |
| `execute` | Run a write statement (INSERT/UPDATE/DELETE/DDL); returns rows affected |
| `list_tables` | List user tables and views (internal `sqlite_*` objects excluded) |
| `describe_table` | Column schema for a table (name, type, NOT NULL, default, PK) |
| `export_csv` | Stream a query's results into a CSV file under root |

</details>

<details>
<summary><b>mcp-sensors</b> — read-only device sensors and status (requires Termux:API)</summary>
<br>

Requires the `termux-api` package and the Termux:API Android app. All tools are
read-only; results pass the API's JSON through verbatim. `get_brightness` reads
the Android settings provider (Termux:API has no brightness getter) and may be
unavailable on some devices.

| Tool | Description |
|------|-------------|
| `get_battery` | Battery level, charging status, health, temperature |
| `get_location` | GPS/network/passive location; `last` returns the cached fix |
| `get_wifi_info` | Current WiFi connection: SSID, IP, link speed, RSSI |
| `get_brightness` | Screen brightness level and auto-brightness mode |
| `get_volume` | Volume of every audio stream |
| `list_sensors` | Tool availability plus the hardware sensor inventory |

</details>

<details>
<summary><b>mcp-notifications</b> — Android notifications and Do Not Disturb (requires Termux:API)</summary>
<br>

Requires the `termux-api` package and the Termux:API Android app. `send` and
`dismiss` have visible side effects but touch no files; running with a key is
recommended. `list_notifications` needs Notification Access granted to
Termux:API. `get_dnd_status` reads the Android settings provider (Termux:API has
no DND getter) and may be unavailable on some devices.

| Tool | Description |
|------|-------------|
| `send_notification` | Post a notification with content, optional title, id, priority |
| `list_notifications` | List active notifications as JSON |
| `dismiss_notification` | Dismiss a notification by id |
| `get_dnd_status` | Do Not Disturb state read from `global zen_mode` |

</details>

<details>
<summary><b>mcp-contacts</b> — read-only Android address book (requires Termux:API)</summary>
<br>

Requires the `termux-api` package and the Termux:API Android app, with Contacts
permission granted. Read-only, so key-less dev mode is allowed on localhost; a
key is recommended because the address book is personal data. The backend
command takes no arguments — every filter is applied in memory, so nothing a
caller types reaches a command line. `list_groups` is a documented stub
(Termux:API has no groups endpoint).

| Tool | Description |
|------|-------------|
| `search_contacts` | Filter by name or number (substring, formatting-insensitive) |
| `get_contact` | Full records for an exact name and/or number |
| `list_groups` | Stub: groups are unavailable via Termux:API |
| `export_contacts` | Export (optionally filtered) as JSON or vCard 3.0 |

</details>

<details>
<summary><b>mcp-sms</b> — read and send Android SMS (requires Termux:API, no dev mode)</summary>
<br>

Requires the `termux-api` package and the Termux:API Android app with SMS
permissions. Highest-privilege Termux:API server: reading messages exposes
OTP/2FA codes and `send_sms` dispatches a **real, billable, irreversible**
message — so it **refuses to start without a key** (`DROIDMCP_SMS_KEY` or
`DROIDMCP_API_KEY`). `send_sms` validates recipients as phone numbers and passes
the body on stdin, so message content never reaches a shell.

| Tool | Description |
|------|-------------|
| `list_sms` | List stored messages by box (inbox/sent/…) with paging |
| `search_sms` | In-memory filter by body text and/or contact number |
| `send_sms` | Send a real SMS to one or more recipients |

</details>

<details>
<summary><b>mcp-llm-proxy</b> — local models through Ollama, no cloud round-trip</summary>
<br>

Requires a running Ollama daemon (`pkg install ollama` or a desktop on the same
LAN). The address comes from `DROIDMCP_OLLAMA_HOST` and **never** from a tool
argument, so a calling model cannot redirect prompts; the default is
`http://127.0.0.1:11434`. A host outside the device or LAN is refused unless
`DROIDMCP_LLMPROXY_ALLOW_REMOTE=1` is set, which keeps prompts from silently
leaving the network; the same rule is re-checked on the dialed IP and no
redirect is ever followed. Responses are returned whole (no streaming), and
on-device generation is slow enough that the default `generate` timeout is 300s.
Dev mode is allowed; the per-server key is `DROIDMCP_LLMPROXY_KEY` (no
separator, like the binary).

| Tool | Description |
|------|-------------|
| `list_models` | Installed models with size, family and quantization |
| `generate` | Single-shot completion with optional system prompt and sampling |
| `embed` | Embedding vector for a piece of text |
| `model_info` | Family, parameter size, context window and capabilities |

</details>

## Quick start

**From a release** — each release ships one binary per server plus a signed
`SHA256SUMS` ([verification details](docs/security.md)):

```bash
curl -LO https://github.com/kahz12/DroidMCP/releases/latest/download/droidmcp-filesystem
curl -LO https://github.com/kahz12/DroidMCP/releases/latest/download/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
chmod +x droidmcp-filesystem && mv droidmcp-filesystem "$PREFIX/bin/"
```

**From source** — on the device ([Termux](https://f-droid.org/en/packages/com.termux/), F-Droid build) or cross-compiled:

```bash
pkg install golang git make          # prerequisites
git clone https://github.com/kahz12/DroidMCP && cd DroidMCP
make build                           # bin/droidmcp-<server>, one per server
make install                         # optional: copy into $PREFIX/bin
```

**Run** — export the server's requirements and start the binary; the MCP stream
is served at `http://localhost:<port>/sse`:

```bash
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"
export DROIDMCP_ROOT="$HOME/workspace"     # never leave this at "/"

DROIDMCP_PORT=3000 droidmcp-filesystem
```

```bash
curl -fsS http://localhost:3000/healthz
# {"status":"ok","server":"mcp-filesystem","version":"v0.2.0"}
```

## Configuration

Every knob is an environment variable prefixed with `DROIDMCP_`. Shared by all
servers:

| Variable | Description | Default |
|----------|-------------|---------|
| `DROIDMCP_PORT` | TCP port the SSE listener binds to | `3000` |
| `DROIDMCP_ROOT` | Filesystem root, validated at startup — required by `filesystem`, `media`, `sqlite` | — |
| `DROIDMCP_API_KEY` | Global key; if set, every request must carry `X-DroidMCP-Key` | unset |
| `DROIDMCP_<SERVER>_KEY` | Per-server key, e.g. `DROIDMCP_TERMUX_KEY`; wins over the global key | unset |
| `DROIDMCP_TLS_CERT` · `DROIDMCP_TLS_KEY` | PEM cert and key; both set enables HTTPS + HSTS | unset |
| `DROIDMCP_LOG_LEVEL` · `DROIDMCP_LOG_FORMAT` | `debug`–`error` · `json` or text | `info` · text |

Per-server variables — GitHub tokens, the `run_command` allowlist, SSRF and
scan-target opt-ins, history caps, `ffmpeg`/`exiftool` paths — are documented in
the [usage guide](docs/usage.md#configuration-reference).

`GET /healthz` always answers without auth so supervisors can probe liveness;
every other route requires the key when one is configured, compared in constant
time.

## Client integration

**Claude Code** — `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "sse",
      "url": "http://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<your-key>" }
    }
  }
}
```

**Gemini CLI** — same endpoint and header, with `uri` in place of `url`. Switch
to `https://…` once TLS is configured.

## Security

The full threat model and production checklist live in
[`docs/security.md`](docs/security.md). The short version:

- **No dev mode for sensitive servers.** `filesystem`, `termux`, `media`, and
  `sqlite` refuse to start without an explicit key — and the first three plus
  `sqlite` also require `DROIDMCP_ROOT`, so an unconfigured server can never act
  on `/` or run unauthenticated.
- **Loopback by design.** Every listener binds `127.0.0.1`; exposure beyond the
  device is an explicit decision that requires a key and TLS.
- **Sandboxed paths.** Absolute paths and `..` are rejected, symlinks are
  resolved and containment re-checked on every path the servers touch;
  `mcp-sqlite` additionally binds all SQL values as parameters.
- **Conservative network defaults.** `mcp-scraper` blocks private/loopback
  targets (SSRF) and `mcp-network` blocks public ones; both are explicit opt-ins.
- **`mcp-termux` is a remote shell.** Restrict it with
  `DROIDMCP_TERMUX_ALLOWLIST`, give it a dedicated key, run it only when needed.
- **Redacted logs, signed releases.** Secret-like attributes are replaced with
  `[REDACTED]`; releases ship `SHA256SUMS` with a cosign signature.

## Development

```
cmd/<server>/       one main package per server (filesystem, github, scraper,
                    termux, network, clipboard, media, sqlite, sensors,
                    notifications, contacts, sms)
internal/           core — shared HTTP/SSE server · config · logger · buildinfo
docs/               usage guide (EN/ES) · security · Termux setup
scripts/            reproducible ARM64 cross-build
```

Built with Go 1.26 on [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go),
[google/go-github](https://github.com/google/go-github),
[gocolly/colly](https://github.com/gocolly/colly) +
[goquery](https://github.com/PuerkitoBio/goquery),
[modernc.org/sqlite](https://gitlab.com/cznic/sqlite), and
[spf13/viper](https://github.com/spf13/viper). CI enforces `gofmt`, `go vet`,
`go test -race`, `golangci-lint`, and `gosec`; tagged releases are built
reproducibly and signed.

Contributions are welcome — read [CONTRIBUTING.md](CONTRIBUTING.md) and check
[ROADMAP.md](ROADMAP.md) for planned work.

## License

Released under the [MIT License](LICENSE).

<div align="center">
<br>

Built on Android, for Android — by Ale.

</div>
