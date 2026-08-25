# DroidMCP Usage Guide

A complete, tool-by-tool reference for operating the DroidMCP servers: how the
transport works, every environment variable, every tool with its exact
parameters and defaults, the JSON each one returns, and end-to-end recipes.

For first-time setup on a device see [`setup-termux.md`](setup-termux.md); for
the threat model and the production checklist see [`security.md`](security.md).
Versión en español: [`usage.es.md`](usage.es.md).

## Contents

- [How the servers work](#how-the-servers-work)
- [Quick start](#quick-start)
- [Configuration reference](#configuration-reference)
- [Talking to a server](#talking-to-a-server)
- [Tool reference](#tool-reference)
  - [mcp-filesystem](#mcp-filesystem)
  - [mcp-github](#mcp-github)
  - [mcp-scraper](#mcp-scraper)
  - [mcp-termux](#mcp-termux)
  - [mcp-network](#mcp-network)
  - [mcp-clipboard](#mcp-clipboard)
  - [mcp-media](#mcp-media)
  - [mcp-sqlite](#mcp-sqlite)
  - [mcp-sensors](#mcp-sensors)
  - [mcp-notifications](#mcp-notifications)
  - [mcp-contacts](#mcp-contacts)
  - [mcp-sms](#mcp-sms)
  - [mcp-llm-proxy](#mcp-llm-proxy)
- [Recipes](#recipes)
- [Troubleshooting](#troubleshooting)

---

## How the servers work

Every DroidMCP binary is a single MCP server that speaks the Model Context
Protocol over HTTP with Server-Sent Events (SSE). They all share the same core,
so the operational behavior below is identical across servers.

**Loopback-only listener.** The listener always binds to `127.0.0.1:<port>` —
never `0.0.0.0`. A server is therefore unreachable from other devices by
default. To expose one deliberately you must put a reverse proxy (or an SSH /
`adb` port-forward) in front of it, at which point authentication and TLS stop
being optional. See [`security.md`](security.md).

**Endpoints.** Each server exposes three routes:

| Route | Auth | Purpose |
|-------|------|---------|
| `GET /sse` | required when a key is set | Opens the long-lived SSE stream. The server replies with an `endpoint` event telling the client where to POST messages. |
| `POST /message` | required when a key is set | Carries JSON-RPC tool calls for an established session. The client learns the exact URL from the `endpoint` event. |
| `GET /healthz` | never | Liveness probe. Always `200`, always unauthenticated. |

**Health check.** `/healthz` returns a small JSON document and bypasses auth so
a supervisor can probe it without holding the key:

```json
{"status":"ok","server":"mcp-filesystem","version":"dev"}
```

`version` is `dev` for local builds; release binaries report the git tag.

**Authentication.** When a key is configured, every request except `/healthz`
must send it in the `X-DroidMCP-Key` header. The comparison is constant-time.
The key is resolved per server: `DROIDMCP_<SERVER>_KEY` is checked first (for
example `DROIDMCP_TERMUX_KEY`), then the global `DROIDMCP_API_KEY`. With no key
set, the low-privilege servers run in **dev mode** — they accept every request
and log `auth=disabled` at startup. `mcp-filesystem`, `mcp-termux`, `mcp-media`,
`mcp-sqlite`, `mcp-github`, and `mcp-sms` have no dev mode: they refuse to start
without a key.

**Host binding.** The listener is bound to `127.0.0.1`, and every request's
`Host` header must name a loopback destination (`localhost`, `127.0.0.1`, `::1`)
or a hostname in `DROIDMCP_ALLOWED_HOSTS`. Anything else gets `403`, so a
malicious web page cannot reach a dev-mode server through DNS rebinding. Set
`DROIDMCP_ALLOWED_HOSTS` (comma-separated, no port) when a reverse proxy or
port-forward fronts the server under another hostname.

**TLS.** Set both `DROIDMCP_TLS_CERT` and `DROIDMCP_TLS_KEY` to PEM files and the
server serves HTTPS and adds an HSTS header. If only one is set, it falls back
to plain HTTP. Response headers `Cache-Control: no-store` and
`X-Content-Type-Options: nosniff` are always sent.

**Timeouts and shutdown.** The read-header timeout is 10s and the idle timeout
is 120s; there is no write timeout because SSE streams are long-lived. On
`SIGINT` or `SIGTERM` the server drains in-flight connections for up to 10s
before exiting, so `Ctrl+C` is a clean stop.

**Logging.** Logs go to stderr. `DROIDMCP_LOG_LEVEL` is one of `debug`, `info`,
`warn`, `error` (default `info`); `DROIDMCP_LOG_FORMAT=json` switches from text
to structured logs. One `http` line is written per request (deferred until the
stream closes for SSE). Sensitive attribute keys (`token`, `secret`, `password`,
`api_key`, `authorization`, `key`) are replaced with `[REDACTED]`, and the
`X-DroidMCP-Key` header is never logged.

---

## Quick start

Build the binaries (full instructions in [`setup-termux.md`](setup-termux.md)):

```bash
git clone https://github.com/kahz12/DroidMCP
cd DroidMCP
make build          # binaries land in bin/
```

Run one server. The filesystem server needs both a root and a key, so it is the
most involved; the rest follow the same shape:

```bash
export DROIDMCP_PORT=3000
export DROIDMCP_ROOT=/storage/emulated/0/Documents
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"
./bin/droidmcp-filesystem
```

Confirm it is alive from a second shell:

```bash
curl -fsS http://localhost:3000/healthz
# {"status":"ok","server":"mcp-filesystem","version":"dev"}
```

Then point an MCP client at `http://localhost:3000/sse` with the key in the
`X-DroidMCP-Key` header (see [Talking to a server](#talking-to-a-server)).

**Running several at once.** Each server needs its own port. In Termux, `tmux`
gives every server a pane, and `Termux:Boot` can export the variables and exec
the binaries at device boot. A convention that matches the rest of the docs:

| Server | Suggested port | Binary |
|--------|:---:|--------|
| filesystem | `3000` | `droidmcp-filesystem` |
| github | `3001` | `droidmcp-github` |
| scraper | `3002` | `droidmcp-scraper` |
| termux | `3003` | `droidmcp-termux` |
| network | `3004` | `droidmcp-network` |
| clipboard | `3005` | `droidmcp-clipboard` |
| media | `3006` | `droidmcp-media` |
| sqlite | `3007` | `droidmcp-sqlite` |
| sensors | `3008` | `droidmcp-sensors` |
| notifications | `3009` | `droidmcp-notifications` |
| contacts | `3010` | `droidmcp-contacts` |
| sms | `3011` | `droidmcp-sms` |
| llm-proxy | `3012` | `droidmcp-llmproxy` |

---

## Configuration reference

Everything is an environment variable prefixed with `DROIDMCP_`. Set it before
launching the binary.

### Shared by every server

| Variable | Default | Notes |
|----------|---------|-------|
| `DROIDMCP_PORT` | `3000` | TCP port for the SSE listener. Must be `1`–`65535` or the server refuses to start. |
| `DROIDMCP_API_KEY` | unset | Global key required in `X-DroidMCP-Key`. Unset means dev mode (except filesystem/termux/media/sqlite/github/sms). |
| `DROIDMCP_<SERVER>_KEY` | unset | Per-server override, e.g. `DROIDMCP_GITHUB_KEY`. Wins over the global key. |
| `DROIDMCP_ALLOWED_HOSTS` | unset | Extra `Host` header values accepted besides loopback (comma-separated, no port). For reverse-proxy / port-forward front-ends. |
| `DROIDMCP_TLS_CERT` | unset | PEM certificate path. Both cert and key must be set to enable HTTPS + HSTS. |
| `DROIDMCP_TLS_KEY` | unset | PEM private key path. |
| `DROIDMCP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `DROIDMCP_LOG_FORMAT` | `text` | `json` for structured logs; any other value is text. |

### Per-server

| Variable | Server | Default | Notes |
|----------|--------|---------|-------|
| `DROIDMCP_ROOT` | filesystem · media · sqlite | none (required) | Directory the server may act on. Must exist and be a directory. filesystem, media, and sqlite refuse to start if unset — the shared default of `/` would expose the whole device. |
| `DROIDMCP_FILESYSTEM_KEY` | filesystem | unset | Required (this or `DROIDMCP_API_KEY`); no dev mode. |
| `DROIDMCP_MAX_READ_BYTES` | filesystem | `10485760` (10 MiB) | Cap on bytes a single `read_file` buffers. Non-numeric or non-positive values are ignored. |
| `GITHUB_TOKEN` | github | none (required) | Personal Access Token. See the three accepted names below. |
| `GITHUB_APP_TOKEN` | github | — | Used if `GITHUB_TOKEN` is unset. |
| `GITHUB_FINE_GRAINED_TOKEN` | github | — | Used if the two above are unset. |
| `DROIDMCP_GITHUB_KEY` | github | unset | Required (this or `DROIDMCP_API_KEY`); no dev mode — the token grants full GitHub API access. |
| `DROIDMCP_SCRAPER_ALLOW_PRIVATE` | scraper | off | Set to `1` to allow loopback / RFC1918 / link-local / CGNAT targets. Off by default for SSRF safety. |
| `DROIDMCP_TERMUX_KEY` | termux | unset | Required (this or `DROIDMCP_API_KEY`); no dev mode. |
| `DROIDMCP_TERMUX_ALLOWLIST` | termux | empty (allow all) | Comma-separated allowlist for `run_command`. Matches the full command or its basename. |
| `DROIDMCP_NETWORK_ALLOW_PUBLIC` | network | off | Set to `1` to allow non-private scan/`check_ports` targets. Off by default. |
| `DROIDMCP_NETWORK_DB` | network | `~/.droidmcp/network-devices.json` | Path to the persistent device inventory JSON. |
| `DROIDMCP_CLIPBOARD_HISTORY_ENTRIES` | clipboard | `32` | Max history entries. Clamped to `1`–`1024`. |
| `DROIDMCP_CLIPBOARD_HISTORY_BYTES` | clipboard | `65536` (64 KiB) | Max total history bytes. Clamped to `1024`–`16777216` (16 MiB). |
| `DROIDMCP_MEDIA_KEY` | media | unset | Required (this or `DROIDMCP_API_KEY`); no dev mode. |
| `DROIDMCP_MEDIA_FFMPEG` | media | PATH lookup | Explicit path to the `ffmpeg` binary used by `convert_image`, `thumbnail`, and `extract_audio`. |
| `DROIDMCP_MEDIA_EXIFTOOL` | media | PATH lookup | Explicit path to `exiftool`; when present, enriches `get_metadata`. |
| `DROIDMCP_SQLITE_KEY` | sqlite | unset | Required (this or `DROIDMCP_API_KEY`); no dev mode. |
| `DROIDMCP_SMS_KEY` | sms | unset | Required (this or `DROIDMCP_API_KEY`); no dev mode — it reads OTP/2FA messages and can send real SMS. |
| `DROIDMCP_OLLAMA_HOST` | llm-proxy | `http://127.0.0.1:11434` | Address of the Ollama daemon. Accepts `host`, `host:port` or a full URL. The IPv4 literal is deliberate: under Termux/proot `localhost` often resolves to `::1` only. |
| `DROIDMCP_LLMPROXY_ALLOW_REMOTE` | llm-proxy | off | Set to `1` to allow a daemon outside the device/LAN. Off by default so prompts do not leave the network; the server refuses to start on a public host without it. |
| `DROIDMCP_LLMPROXY_KEY` | llm-proxy | unset | Per-server key. Note the name has no separator: the server is `mcp-llm-proxy` but the variable is `LLMPROXY`, matching the `cmd/llmproxy` binary. A misspelling is not an error — the server starts in dev mode with auth disabled. |

The GitHub token is resolved in order: `GITHUB_TOKEN`, then `GITHUB_APP_TOKEN`,
then `GITHUB_FINE_GRAINED_TOKEN`. The first one set is used and validated at
startup with a `GET /user` call; a bad token fails the server immediately
instead of failing every tool call later.

---

## Talking to a server

DroidMCP implements the MCP SSE transport, so the ergonomic path is to use an
MCP-aware client and let it manage the session. Two examples:

**Claude Code** — add to `~/.claude/settings.json`:

```jsonc
{
  "mcpServers": {
    "filesystem": {
      "type": "sse",
      "url": "http://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<paste-the-key>" }
    }
  }
}
```

**Gemini CLI** — same endpoint, `uri` instead of `url`:

```jsonc
{
  "mcpServers": {
    "filesystem": {
      "uri": "http://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<paste-the-key>" }
    }
  }
}
```

Switch the scheme to `https://` once TLS is configured.

**Inspecting by hand.** The session handshake (open `/sse`, read the `endpoint`
event, POST an `initialize` request, then `tools/call` messages) is tedious to
drive with `curl`. Use the [MCP Inspector](https://github.com/modelcontextprotocol/inspector)
or any MCP client for interactive testing. The only endpoint worth probing with
`curl` directly is the unauthenticated health check:

```bash
curl -fsS http://localhost:3000/healthz
```

In the tool reference below, arguments are the JSON object a client sends as the
`arguments` of a `tools/call` request. "Required" means the call fails with an
error result if the argument is missing.

---

## Tool reference

Notation for every table: **Required** marks arguments that must be present;
**Default** is the value used when the argument is omitted. Unless stated
otherwise, string paths in `mcp-filesystem` are relative to `DROIDMCP_ROOT`.

### mcp-filesystem

Sandboxed file operations under `DROIDMCP_ROOT`. Paths are validated on every
call: absolute paths are rejected, `..` traversal is rejected, and symlinks are
resolved and re-checked so a link inside the root cannot point outside it. This
server requires both `DROIDMCP_ROOT` and a key, and has no dev mode.

**`read_file`** — read a file, optionally a byte range.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `path` | string | yes | — | File path relative to root. |
| `offset` | number | no | `0` | Byte offset to start reading at. Must be non-negative. |
| `length` | number | no | `0` | Max bytes to read; `0` reads to end. Must be non-negative and not exceed `DROIDMCP_MAX_READ_BYTES`. |

An unbounded read of a file larger than the cap returns an error telling you to
page it with `offset`/`length` rather than silently truncating.

**`read_file_lines`** — read a 1-indexed inclusive line range.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `path` | string | yes | — | File path relative to root. |
| `start` | number | yes | — | First line (1-indexed). Must be `>= 1`. |
| `end` | number | no | `0` | Last line, inclusive. `0` means end of file; otherwise must be `>= start`. |

**`write_file`** — write or create a file. Parent directories are created
(`0755`); the file is written `0644`, overwriting any existing content.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `path` | string | yes | File path relative to root. |
| `content` | string | yes | Content to write. |

**`list_directory`** — list a directory as a JSON array of entries. Each entry
is `{name, type, size, mode, mode_octal, modified, uid, gid}` where `type` is
`file`, `dir`, `symlink`, or `other`, `modified` is RFC3339 UTC, and `uid`/`gid`
are present only on Unix.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `path` | string | yes | Directory path relative to root. |

**`stat`** — metadata for a single path, same shape as one `list_directory`
entry. Uses `Lstat`, so a symlink is reported as `symlink` rather than followed.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `path` | string | yes | Path relative to root. |

**`search_files`** — recursive name search. Provide exactly one of `pattern` or
`regex`; the pattern/regex is matched against each entry's name. Returns paths
relative to the search root, one per line, or `No matches found`.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `root` | string | no | `.` | Directory to start from (relative to root). |
| `pattern` | string | one of | — | Glob (`filepath.Match` syntax). Mutually exclusive with `regex`. |
| `regex` | string | one of | — | Regular expression. Mutually exclusive with `pattern`. |
| `max_results` | number | no | `0` | Stop after this many matches; `0` is unlimited. |

**`delete_file`** — delete a file or directory.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `path` | string | yes | — | Path relative to root. |
| `recursive` | boolean | no | `false` | Remove non-empty directories recursively. Without it, a non-empty directory errors with a hint. |

**`move_file`** — move or rename. Backed by `os.Rename`, so source and
destination must be on the same filesystem.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `source` | string | yes | Source path relative to root. |
| `destination` | string | yes | Destination path relative to root. |

**`copy_file`** — copy a file, or recursively copy a directory tree. File modes
are preserved; symlinks encountered during a directory copy are skipped.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `source` | string | yes | Source path relative to root. |
| `destination` | string | yes | Destination path relative to root. |

### mcp-github

Full GitHub operations via a token, built on `google/go-github`. Because the
token can read private repos and push commits/PRs, this server **requires a key
and has no dev mode** (`DROIDMCP_GITHUB_KEY` or `DROIDMCP_API_KEY`). List calls
accept `per_page` (max 100, default 30) and `page` (default 1); responses embed
a `_rate_limit` block, and a rate-limit error surfaces the reset time so an
agent can back off. `owner` and `repo` are required on every repo-scoped tool.

**Repositories**

| Tool | Required args | Optional args |
|------|---------------|---------------|
| `list_repos` | — | `per_page`, `page` |
| `get_repo` | `owner`, `repo` | — |
| `list_branches` | `owner`, `repo` | `protected_only` (bool), `per_page`, `page` |
| `list_tags` | `owner`, `repo` | `per_page`, `page` |
| `list_releases` | `owner`, `repo` | `per_page`, `page` |
| `list_commits` | `owner`, `repo` | `sha` (SHA/branch to start from), `path` (only commits touching it), `author`, `per_page`, `page` |
| `get_commit` | `owner`, `repo`, `sha` (SHA, branch, or tag) | — |
| `fork_repo` | `owner`, `repo` | `organization` (fork into), `name` (rename fork), `default_branch_only` (bool) |

**Issues**

| Tool | Required args | Optional args |
|------|---------------|---------------|
| `create_issue` | `owner`, `repo`, `title` | `body` |
| `list_issues` | `owner`, `repo` | `state` (`open` default, `closed`, `all`), `per_page`, `page` |
| `comment_issue` | `owner`, `repo`, `number`, `body` | — |
| `close_issue` | `owner`, `repo`, `number` | `state_reason` (`completed` default, `not_planned`) |
| `label_issue` | `owner`, `repo`, `number`, `labels` (array) | `replace` (bool: replace vs append) |

`comment_issue` works on both issues and pull requests (a PR is an issue on the
GitHub API).

**Pull requests**

| Tool | Required args | Optional args |
|------|---------------|---------------|
| `get_pr` | `owner`, `repo`, `number` | — |
| `create_pr` | `owner`, `repo`, `title`, `head`, `base` | `body`, `draft` (bool) |
| `review_pr` | `owner`, `repo`, `number`, `event` (`APPROVE`, `REQUEST_CHANGES`, `COMMENT`) | `body` (required when `event` is `REQUEST_CHANGES`) |
| `merge_pr` | `owner`, `repo`, `number` | `commit_title`, `commit_message`, `merge_method` (`merge` default, `squash`, `rebase`), `sha` (merge only if head matches) |

**Files**

| Tool | Required args | Optional args |
|------|---------------|---------------|
| `get_file` | `owner`, `repo`, `path` | `ref` (commit/branch/tag; default the repo's default branch). Base64 is auto-decoded. |
| `commit_file` | `owner`, `repo`, `path`, `content`, `message` | `branch` (default the repo's default branch). Creates or updates the file via the Content API. |

**Search**

| Tool | Required args | Optional args |
|------|---------------|---------------|
| `search_code` | `query` | `sort` (`indexed`), `order` (`asc`/`desc`, default `desc`), `per_page`, `page`. Query uses GitHub search syntax, e.g. `language:go addr in:file repo:owner/name`. |
| `search_issues` | `query` | `sort` (`comments`/`created`/`updated`), `order`, `per_page`, `page`. Searches issues and PRs. |

### mcp-scraper

Chromium-free scraping on `colly` + `goquery`. **SSRF protection is on by
default**: targets resolving to loopback, RFC1918, IPv6 ULA, link-local,
multicast, or CGNAT ranges are refused unless `DROIDMCP_SCRAPER_ALLOW_PRIVATE=1`.
Responses are cached in an in-memory LRU with a 5-minute TTL; response bodies are
capped at 10 MiB.

**Common arguments** — accepted by every scraper tool:

| Argument | Type | Default | Description |
|----------|------|---------|-------------|
| `headers` | object | — | Map of request header name to value. |
| `user_agent` | string | — | Override the `User-Agent` for this request. |
| `timeout_seconds` | number | `20` | Per-request timeout. Max `60`. |
| `no_cache` | boolean | `false` | Bypass the response cache for this call. |
| `wait_selector` | string | — | Retry the fetch until this CSS selector matches (helps with server-rendered/lazy pages). |
| `wait_attempts` | number | `3` | Max retries when `wait_selector` is set. Max `10`. |
| `wait_interval_ms` | number | `1000` | Delay between retries when `wait_selector` is set. |

**Tool-specific arguments:**

| Tool | Required args | Extra optional args | Returns |
|------|---------------|---------------------|---------|
| `fetch_page` | `url` (http/https only) | — | JSON `{url, status, headers, body, ...}`. |
| `extract_text` | `url` | `selector` (default `<body>`) | Clean visible text. |
| `extract_links` | `url` | `selector` (default `a[href]`) | Absolute URLs with anchor text and `rel`. |
| `extract_table` | `url` | `selector` (default `table`) | Tables as structured JSON. |
| `extract_metadata` | `url` | — | Title, description, canonical, `og:*`, `twitter:*`. |
| `search_in_page` | `url`, `query` | `regex` (bool), `case_sensitive` (bool), `selector` (default `<body>`), `max_results` (default `20`, max `100`), `context_chars` (default `80`, max `500`) | Matches with surrounding context. |

For `search_in_page`, `query` is literal text unless `regex` is `true`, in which
case it is a Go regular expression; matching is case-insensitive unless
`case_sensitive` is `true`.

### mcp-termux

Direct access to the Termux environment. This server hands the caller real
authority over the device, so it **requires a key and has no dev mode**. The
`termux_*` wrapper tools additionally need the `termux-api` package and the
Termux:API app (see [`setup-termux.md`](setup-termux.md)).

**`run_command`** — execute a program. It runs the binary directly with no shell,
so there is no glob/pipe/redirect expansion: pass arguments through `args`, not
as a single string. Returns JSON `{stdout, stderr, exit_code, ...}`; each output
stream is capped at 1 MiB.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `command` | string | yes | — | The program to execute (no shell). |
| `args` | string[] | no | `[]` | Arguments passed to the program. |
| `cwd` | string | no | — | Working directory for the child process. |
| `env_extra` | object | no | — | Extra environment variables on top of the parent env. Dynamic-linker overrides (`LD_*`, `DYLD_*`) are rejected — they could bypass the allowlist. |
| `timeout_seconds` | number | no | `30` | Per-call timeout. Max `300`. |

When `DROIDMCP_TERMUX_ALLOWLIST` is set, `command` must match one of its
comma-separated entries (by full value or basename) or the call is refused. An
empty/unset allowlist allows any command. Note that the allowlist is a coarse
control: an allowlisted interpreter (`sh`, `python`, …) can still run other
code, so treat it as reducing blast radius rather than a strict sandbox.

**`install_pkg`** — install a package via `pkg install -y`.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `package` | string | yes | — | Package name. |
| `timeout_seconds` | number | no | `30` | Per-call timeout. Max `300`. |

**`list_pkgs`** — list installed packages. No arguments.

**`read_env`** — read environment variables. Returns `{name, value}` for a named
variable, or `{vars: {...}}` when `name` is omitted.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `name` | string | no | Variable to read; omit to list all. |

**`get_storage`** — storage usage (total/used/available bytes). With no `path`,
reports Termux home, prefix, and shared storage.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `path` | string | no | Inspect this path instead of the default set. |

**Termux:API wrappers** (need `termux-api`):

| Tool | Required args | Optional args |
|------|---------------|---------------|
| `termux_battery_status` | — | `timeout_seconds` |
| `termux_location` | — | `provider` (`gps` default, `network`, `passive`), `request` (`once` default, `last`, `updates`), `timeout_seconds` |
| `termux_notification` | — | `title`, `content`, `id` (reuse to replace a prior notification) |
| `termux_toast` | `text` | — |
| `termux_sms_send` | `number`, `text` | — (needs SMS permission granted to Termux:API) |
| `termux_tts_speak` | `text` | `language` (BCP47, e.g. `en-US`), `rate` (`1.0` = normal), `pitch` (`1.0` = normal) |

### mcp-network

LAN discovery via concurrent TCP probes. **Targets must be in a private range by
default**; set `DROIDMCP_NETWORK_ALLOW_PUBLIC=1` to permit public hosts. Scan
results are persisted to the inventory at `DROIDMCP_NETWORK_DB` (default
`~/.droidmcp/network-devices.json`) and are what `list_devices` /
`get_device_info` read back.

**`scan_network`** — scan a subnet for active hosts. Returns JSON per host with
IP, MAC (from ARP), and open ports, and records them in the inventory.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `subnet` | string | no | auto-detected | CIDR to scan, e.g. `192.168.1.0/24`. Empty auto-detects the local subnet from the kernel's interface mask. |
| `timeout_seconds` | number | no | `30` | Per-call timeout. Max `120`. |

**`check_ports`** — concurrent TCP port check on one host. Returns
`{host, resolved, ports: [{port, open}]}`.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `host` | string | yes | — | IP or hostname. |
| `ports` | string | no | common set | Comma-separated ports. The default set is `21,22,23,25,53,80,110,135,139,143,443,445,993,995,1723,3306,3389,5900,8080`. |
| `timeout_seconds` | number | no | `15` | Per-call timeout. Max `60`. |

**`nslookup`** — forward DNS. Returns `{host, addrs}`.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `host` | string | yes | Hostname to resolve. |

**`reverse_dns`** — reverse DNS. Returns `{ip, names}`.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `ip` | string | yes | IP address to look up. |

**`traceroute`** — trace the path to a host. Shells out to `traceroute` or
`tracepath`; the latter needs no root.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `host` | string | yes | — | Target host. |
| `max_hops` | number | no | `30` | Max TTL hops to probe. |
| `timeout_seconds` | number | no | `30` | Per-call timeout. Max `120`. |

**`network_info`** — local metadata: default gateway (from `/proc/net/route`),
DNS servers, interfaces, and detected subnet. No arguments.

**`list_devices`** — every device remembered from prior `scan_network` runs. No
arguments. Empty until you run a scan.

**`get_device_info`** — remembered details (MAC, open ports, first/last seen)
for one device.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `device` | string | yes | IP or MAC address seen in a previous scan. |

### mcp-clipboard

Clipboard bridge between Android and the agent. Requires the `termux-api`
package and the Termux:API app; without them the tools fail with a hint naming
the missing step. Writes are also recorded in a bounded in-process history
(default 32 entries / 64 KiB, configurable via the two `..._HISTORY_...`
variables).

**`get_clipboard`** — read the clipboard. Returns
`{text, bytes_len, base64, is_utf8, truncated}`; binary content is recoverable
from `base64`. No arguments.

**`set_clipboard`** — write the clipboard. Provide exactly one of `text` or
`text_base64`; providing both or neither is an error.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `text` | string | one of | UTF-8 text to write. |
| `text_base64` | string | one of | Base64-encoded bytes, for non-UTF-8/binary content. |

**`clear_clipboard`** — clear the system clipboard and the in-process history.
Returns `{ok, history_cleared}`. No arguments.

**`clipboard_history`** — the in-process history of writes, oldest first. No
arguments.

### mcp-media

Browsing and transformation of on-device media under `DROIDMCP_ROOT`. Paths are
validated exactly as in `mcp-filesystem` — absolute paths and `..` are rejected,
and symlinks are resolved and re-checked so a link inside the root cannot point
outside it. Like filesystem, this server **requires both `DROIDMCP_ROOT` and a
key and has no dev mode**: it reads and writes files and spawns subprocesses.
`list_media` and image dimensions are pure Go; the transform tools shell out to
`ffmpeg` (`pkg install ffmpeg`), and `get_metadata` is enriched by `exiftool`
when it is installed. Every `path`/`source`/`destination` is relative to
`DROIDMCP_ROOT`.

**`list_media`** — list media files under a directory. Returns a JSON array of
`{name, path, type, ext, size, modified}` where `type` is `image`, `video`, or
`audio`, `path` is relative to root, and `modified` is RFC3339 UTC. Non-media
files are skipped.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `path` | string | no | `.` | Directory to scan, relative to root. |
| `types` | string[] | no | all kinds | Filter by kind: any of `image`, `video`, `audio`. |
| `recursive` | boolean | no | `false` | Descend into subdirectories. |
| `max_results` | number | no | `0` | Stop after this many matches; `0` is unlimited. |

**`get_metadata`** — metadata for one media file. Always returns
`{path, type, ext, size, modified}`; adds `width`/`height` for images the stdlib
can decode a header for (JPEG, PNG, GIF), and an `exif` object with the full tag
set when `exiftool` is installed (absolute-path fields are stripped).

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `path` | string | yes | Media file relative to root. |

**`convert_image`** — convert an image and/or resize it via `ffmpeg`. The output
format is taken from the destination extension. Returns JSON
`{ok, tool, source, destination, exit_code, duration_ms}`; a non-zero exit
surfaces the tail of ffmpeg's stderr, and any output file the failed run created
is removed (`partial_output_removed: true`) — a destination that already existed
before the call is never deleted by cleanup.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `source` | string | yes | — | Source image relative to root. |
| `destination` | string | yes | — | Destination relative to root; the extension picks the format. Must differ from `source`. |
| `width` | number | no | `0` | Target width in px. `0` keeps aspect from `height` (or the original when both are `0`). |
| `height` | number | no | `0` | Target height in px. `0` keeps aspect from `width`. |
| `quality` | number | no | — | Quality `1`–`100` (higher is better). Applied to JPEG destinations only. |
| `timeout_seconds` | number | no | `120` | Per-call timeout. Max `600`. |

**`thumbnail`** — a single scaled frame from an image or video via `ffmpeg`. For
video, the frame is grabbed at `timestamp`. Same result shape as `convert_image`.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `source` | string | yes | — | Source media relative to root. |
| `destination` | string | yes | — | Destination image relative to root. Must differ from `source`. |
| `width` | number | no | `320` | Thumbnail width in px (height auto when omitted). |
| `height` | number | no | `0` | Thumbnail height in px; `0` keeps aspect ratio. |
| `timestamp` | string | no | `0` | For video: seek position, seconds (`5`) or `HH:MM:SS` (`00:00:05`). |
| `timeout_seconds` | number | no | `120` | Per-call timeout. Max `600`. |

**`extract_audio`** — extract the audio track from a video via `ffmpeg -vn`. By
default the stream is copied without re-encoding. Same result shape as
`convert_image`.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `source` | string | yes | — | Source video relative to root. |
| `destination` | string | yes | — | Destination audio relative to root. Must differ from `source`. |
| `codec` | string | no | `copy` | Audio codec, e.g. `mp3`, `aac`, `flac`. `copy` re-muxes without re-encoding. |
| `bitrate` | string | no | — | Target bitrate when re-encoding, e.g. `192k`. Ignored when `codec` is `copy`. |
| `timeout_seconds` | number | no | `120` | Per-call timeout. Max `600`. |

The transform tools fail with an install hint when `ffmpeg` is not found. Set
`DROIDMCP_MEDIA_FFMPEG` / `DROIDMCP_MEDIA_EXIFTOOL` to pin a specific binary.

---

### mcp-sqlite

Local SQLite databases stored as files under `DROIDMCP_ROOT`. The engine is
`modernc.org/sqlite`, a pure-Go implementation, so the binary needs no CGO and no
`libsqlite3`. Paths are validated exactly as in `mcp-filesystem` — absolute paths
and `..` are rejected, and symlinks are resolved and re-checked. Like filesystem,
this server **requires both `DROIDMCP_ROOT` and a key and has no dev mode**: it
creates files and executes arbitrary SQL.

Every `db` and `destination` is relative to `DROIDMCP_ROOT`. All user values must
be passed through the `args` array rather than being formatted into the SQL text:
they are bound as parameters, which is what keeps statements injection-safe. Use
`?` placeholders in `sql`, one per element of `args`, in order. Numbers, strings,
booleans, and `null` are accepted; an integral number binds as an integer.

Only `open_db` creates a database — `query`, `execute`, `list_tables`,
`describe_table`, and `export_csv` return an error for a path that does not exist
yet, so a typo can never silently create an empty database. Connections are pooled
and reused across calls within a running server, with a single writer so
concurrent calls do not race on the file.

**`open_db`** — open a database, creating the file (and any missing parent
directories) if it does not exist. Returns `{path, created, sqlite_version}`,
where `created` is `true` only when this call made the file. Calling it first is
optional; the other tools open an existing database lazily.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `db` | string | yes | Database file path relative to root, e.g. `data/app.db`. |

**`query`** — run a read-only statement and return its rows. It executes on a
connection opened read-only (`mode=ro`), so the SQLite engine rejects any write
— including one stacked after a `SELECT` (`SELECT 1; DELETE …`) or fronted by a
CTE (`WITH … DELETE …`); the leading-keyword check (`SELECT`, `WITH`, `PRAGMA`,
`EXPLAIN`, `VALUES`) is just a friendlier early error pointing you at `execute`.
Returns `{columns, rows, count, truncated}`, where `rows` is a JSON array of
column-keyed objects and `truncated` is `true` when more rows existed than
`max_rows` allowed. TEXT/BLOB values are returned as strings.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `db` | string | yes | — | Database file path relative to root. Must already exist. |
| `sql` | string | yes | — | The statement; use `?` placeholders for values. |
| `args` | any[] | no | none | Positional parameters bound to the `?` placeholders, in order. |
| `max_rows` | number | no | `1000` | Cap on returned rows; `0` means unlimited. |
| `timeout_seconds` | number | no | `30` | Per-call timeout. Max `600`. |

**`execute`** — run a write statement (`INSERT`/`UPDATE`/`DELETE`, DDL such as
`CREATE`/`DROP`/`ALTER`, etc.). Returns `{rows_affected, last_insert_id}` where
the driver reports them.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `db` | string | yes | — | Database file path relative to root. Must already exist. |
| `sql` | string | yes | — | The statement; use `?` placeholders for values. |
| `args` | any[] | no | none | Positional parameters bound to the `?` placeholders, in order. |
| `timeout_seconds` | number | no | `30` | Per-call timeout. Max `600`. |

**`list_tables`** — list the user tables and views in a database. Internal
`sqlite_*` objects are excluded. Returns a JSON array of `{name, type}` where
`type` is `table` or `view`, ordered by name.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `db` | string | yes | Database file path relative to root. Must already exist. |

**`describe_table`** — describe a table's columns via `PRAGMA table_info`. The
table name is validated against the schema before use, so it cannot be an
injection vector. Returns `{table, columns}` where each column is
`{cid, name, type, notnull, default, pk}`.

| Argument | Type | Required | Description |
|----------|------|:---:|-------------|
| `db` | string | yes | Database file path relative to root. Must already exist. |
| `table` | string | yes | Name of the table or view to describe. |

**`export_csv`** — run a read statement and stream its results into a CSV file
under root (a header row plus one row per record). Like `query` it runs on a
read-only (`mode=ro`) connection, so a write cannot slip in through the `sql`
argument. Returns `{path, rows, columns}`. The destination's parent directories
are created; a failure mid-stream removes only a file this call created, never
pre-existing data, and the destination must differ from the source database.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `db` | string | yes | — | Database file path relative to root. Must already exist. |
| `sql` | string | yes | — | The `SELECT` whose rows are exported; use `?` placeholders for values. |
| `destination` | string | yes | — | Destination CSV path relative to root. Must differ from `db`. |
| `args` | any[] | no | none | Positional parameters bound to the `?` placeholders, in order. |
| `timeout_seconds` | number | no | `30` | Per-call timeout. Max `600`. |

---

### mcp-sensors

Read-only access to device sensors and status through Termux:API. Every tool
needs the `termux-api` package (`pkg install termux-api`) plus the Termux:API
Android app; a missing piece surfaces as an install hint. Nothing on the device
is modified, so — like `mcp-clipboard` — the server allows key-less dev mode on
localhost (set `DROIDMCP_SENSORS_KEY` or `DROIDMCP_API_KEY` to require auth).
Location data is privacy-sensitive: prefer running this server with a key.

Successful calls pass the Termux:API JSON through verbatim; failed calls return
the full run record (`stdout`, `stderr`, `exit_code`, `timed_out`). Every tool
accepts `timeout_seconds` (default 15s — 30s for `get_location` — max 120s).

**`get_battery`** — battery status via `termux-battery-status`. Returns the
API's JSON: `health`, `percentage`, `plugged`, `status`, `temperature`,
`current`.

**`get_location`** — device location via `termux-location`. Returns the API's
JSON: `latitude`, `longitude`, `altitude`, `accuracy`, `bearing`, `speed`,
`provider`. A fresh GPS fix can take tens of seconds indoors; `request: "last"`
returns the cached fix immediately.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `provider` | string | no | `network` | `gps`, `network`, or `passive`. |
| `request` | string | no | `once` | `once` (fresh fix) or `last` (cached, immediate). `updates` is rejected: it streams forever. |
| `timeout_seconds` | number | no | `30` | Per-call timeout. Max `120`. |

**`get_wifi_info`** — current WiFi connection via `termux-wifi-connectioninfo`.
Returns the API's JSON: SSID, BSSID, IP, link speed, RSSI, frequency.

**`get_brightness`** — screen brightness. Termux:API has no brightness getter
(`termux-brightness` only sets), so this reads the Android settings provider and
returns `{brightness (0-255), auto, source}`. On devices that restrict the
settings provider the tool errors with an explanation instead of guessing.

**`get_volume`** — volume of every audio stream via `termux-volume`. Returns
the API's JSON array: `[{stream, volume, max_volume}, …]`.

**`list_sensors`** — availability report plus hardware inventory. Returns
`{tools: {<tool>: {backend, available}}, hardware}` where `hardware` is the
`termux-sensor -l` sensor list when that wrapper is present (a `hardware_error`
string explains its absence otherwise; the availability map is still returned).

---

### mcp-notifications

Android notifications and Do Not Disturb status through Termux:API. Every tool
needs the `termux-api` package (`pkg install termux-api`) plus the Termux:API
Android app; a missing piece surfaces as an install hint. `send_notification`
and `dismiss_notification` have visible side effects but touch no files and run
no arbitrary code, so — like `mcp-clipboard` — the server allows key-less dev
mode on localhost (set `DROIDMCP_NOTIFICATIONS_KEY` or `DROIDMCP_API_KEY` to
require auth). Because posting notifications is user-visible, prefer running this
server with a key outside local development. Every tool accepts `timeout_seconds`
(default 15s, max 120s).

**`send_notification`** — post a notification via `termux-notification`. Returns
`{sent, id}`; `termux-notification` prints nothing on success, so a
system-assigned id is not echoed — pass your own `id` to update or later dismiss
the notification.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `content` | string | yes | — | Notification body text. |
| `title` | string | no | — | Notification title. |
| `id` | string | no | — | Stable id; reuse to update, then pass to `dismiss_notification`. |
| `priority` | string | no | `default` | One of `min`, `low`, `default`, `high`, `max`. |
| `timeout_seconds` | number | no | `15` | Per-call timeout. Max `120`. |

**`list_notifications`** — active notifications via `termux-notification-list`.
Returns the API's JSON array (`id`, `tag`, `key`, `group`, `packageName`,
`title`, `content`, `when`). Requires the **Notification Access** permission to
be granted to Termux:API in Android settings; without it the list is empty.

**`dismiss_notification`** — remove a notification via
`termux-notification-remove`. Takes a required `id` (one previously passed to
`send_notification`) and returns `{dismissed, id}`.

**`get_dnd_status`** — Do Not Disturb state. Termux:API has no DND getter, so
this reads `global zen_mode` from the Android settings provider and returns
`{dnd_enabled, mode, zen_mode, source}`, where `mode` is `off`,
`priority_only`, `total_silence`, or `alarms_only`. On devices that restrict the
settings provider the tool errors with an explanation instead of guessing.

---

### mcp-contacts

Read-only access to the Android address book through Termux:API
(`termux-contact-list`). Every tool needs the `termux-api` package
(`pkg install termux-api`) plus the Termux:API Android app, and Contacts
permission granted to it; a missing piece surfaces as an install hint. The
backend command takes **no arguments** — every filter (`query`, `name`,
`number`) is applied in memory, so nothing a caller types ever reaches a command
line. All tools are read-only, so — like `mcp-sensors` — the server allows
key-less dev mode on localhost (set `DROIDMCP_CONTACTS_KEY` or
`DROIDMCP_API_KEY` to require auth). Because the address book is personal data,
prefer running this server with a key outside local development. The tools that
call the backend accept `timeout_seconds` (default 15s, max 120s).

Termux:API's `ContactList` reports a `name` and a single phone `number` per
entry; those are the fields every tool returns.

**`search_contacts`** — filter the address book. `query` matches as a
case-insensitive substring of the contact name, or against the phone number with
spaces, dashes, dots and parentheses ignored on both sides. Returns
`{count, contacts:[{name, number}, …]}`.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `query` | string | yes | — | Text matched against name or number. |
| `limit` | number | no | `50` | Max contacts returned. Clamped to `1`–`500`. |
| `timeout_seconds` | number | no | `15` | Per-call timeout. Max `120`. |

**`get_contact`** — fetch full records for a specific contact. Provide `name`
(exact, case-insensitive) and/or `number` (formatting ignored); at least one is
required and, when both are given, both must match. Returns
`{found, count, contacts:[…]}` — an array, since several entries can share a
name. `found` is `false` with an empty array when nothing matches (not an
error).

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `name` | string | one of name/number | — | Exact contact name (case-insensitive). |
| `number` | string | one of name/number | — | Exact phone number (formatting ignored). |
| `timeout_seconds` | number | no | `15` | Per-call timeout. Max `120`. |

**`list_groups`** — documented stub. Termux:API exposes no contact-groups
endpoint, so this returns `{supported:false, groups:[], note}` rather than
fabricated data. Use `search_contacts` or `export_contacts` instead.

**`export_contacts`** — export the address book, optionally filtered by `query`
(same matching as `search_contacts`), as JSON or vCard 3.0. `format` is `json`
(default) — returns `{format, count, contacts:[…]}` — or `vcard`, which returns
`{format, count, vcard}` where `vcard` is ready-to-save `.vcf` text (each record
`BEGIN:VCARD`/`VERSION:3.0`/`FN`/`TEL`/`END:VCARD`, with values escaped per
RFC 6350). Contacts without a number omit the `TEL` line.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `format` | string | no | `json` | `json` or `vcard`. |
| `query` | string | no | — | Optional filter; matches name or number. |
| `timeout_seconds` | number | no | `15` | Per-call timeout. Max `120`. |

---

### mcp-sms

Android SMS through Termux:API (`termux-sms-list`, `termux-sms-send`). Every tool
needs the `termux-api` package (`pkg install termux-api`) plus the Termux:API
Android app, with the SMS permissions granted to it; a missing piece surfaces as
an install hint. This is the **highest-privilege** Termux:API server, so it has
**no dev mode** — it refuses to start without `DROIDMCP_SMS_KEY` or
`DROIDMCP_API_KEY`, because reading messages exposes one-time passcodes and 2FA
codes, and `send_sms` dispatches a real, billable, irreversible message. Every
tool accepts `timeout_seconds` (default 15s, max 120s).

**`list_sms`** — stored messages via `termux-sms-list`. Returns the API's JSON
array (fields such as `threadid`, `type`, `read`, `number`, `received`, `body`).
Treat the output as sensitive — it can contain OTP/2FA codes.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `type` | string | no | `all` | Message box: `all`, `inbox`, `sent`, `draft`, `outbox`, `failed`, `queued`. |
| `limit` | number | no | `10` | Max messages returned. Clamped to `1`–`500`. |
| `offset` | number | no | `0` | Messages to skip (paging). |
| `timeout_seconds` | number | no | `15` | Per-call timeout. Max `120`. |

**`search_sms`** — fetch a page via `termux-sms-list` and filter it in memory.
`query` matches the message body (case-insensitive substring); `number` matches
the address with spaces, dashes and parentheses ignored on both sides. At least
one of `query`/`number` is required; when both are given, both must match.
Returns `{count, messages:[…]}`. The filters never touch a command line.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `query` | string | one of query/number | — | Text matched in the body (case-insensitive). |
| `number` | string | one of query/number | — | Address matched (formatting ignored). |
| `type` | string | no | `all` | Message box to scan (same values as `list_sms`). |
| `limit` | number | no | `100` | Max messages scanned/returned. Clamped to `1`–`500`. |
| `timeout_seconds` | number | no | `15` | Per-call timeout. Max `120`. |

**`send_sms`** — send a real SMS via `termux-sms-send`. **This dispatches a
billable, irreversible message**, so use it deliberately. `number` is one or
more recipients (comma-separated; each is digits with an optional leading `+` —
anything else is rejected); `text` is the body, delivered to the command on
**stdin**, never as an argument, so message content can never be parsed as an
option or reach a shell. Returns `{sent, recipients, sim_slot}`. Requires the
`SEND_SMS` permission granted to Termux:API.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `number` | string | yes | — | Recipient(s), comma-separated. Digits with optional leading `+`. |
| `text` | string | yes | — | Message body (max 4000 characters). |
| `sim_slot` | number | no | — | 0-based SIM slot to send from. |
| `timeout_seconds` | number | no | `15` | Per-call timeout. Max `120`. |

---

### mcp-llm-proxy

Local inference through an Ollama daemon (`pkg install ollama`, then `ollama
serve`). Nothing is generated in this process: each tool is one guarded call to
one Ollama endpoint, with the reply trimmed to the fields an agent can act on.

The daemon address comes from `DROIDMCP_OLLAMA_HOST` and **never** from a tool
argument, so the model being served cannot redirect its own prompts. A bare
`host`, a `host:port` pair, or a full URL are all accepted; the default is
`http://127.0.0.1:11434`, pinned to the IPv4 literal because under Termux and
proot `localhost` frequently resolves to `::1` only, where Ollama is not
listening. At startup the resolved address must be loopback, RFC1918,
link-local or CGNAT — a public address is refused unless
`DROIDMCP_LLMPROXY_ALLOW_REMOTE=1` is set, so prompts never leave the network by
accident. Two more checks back that up: the same rule is re-applied to the
concrete IP at dial time (so a hostname that starts resolving elsewhere is
caught mid-run), and redirects are never followed, because Go replays the
request body on a 307 and a daemon answering with a `Location` header could
otherwise bounce every prompt to an unvetted host.

Dev mode is allowed: the server reads no device data and writes nothing, though
a key is still worth setting on a shared device because generation costs CPU and
battery. The variable is **`DROIDMCP_LLMPROXY_KEY`** — no separator, matching the
`droidmcp-llmproxy` binary rather than the `mcp-llm-proxy` server name. Spelling
it `DROIDMCP_LLM_PROXY_KEY` is not an error: the key is simply not found and the
server starts unauthenticated.

Streaming is off by design (an MCP call returns a single payload), so `generate`
answers once the whole completion is ready. On-device generation is slow —
seconds for a 0.5B model, minutes for larger ones — hence the 300s default
timeout. Every tool accepts `timeout_seconds`, capped at `900`.

**`list_models`** — the models the daemon can actually run, via `GET /api/tags`.
Returns `{count, models:[{name, size_bytes, family, parameter_size,
quantization, modified_at}]}`. Call it first: the `model` argument of the other
tools must match one of these names exactly (tag included).

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `timeout_seconds` | number | no | `15` | Per-call timeout. Max `900`. |

**`generate`** — a single-shot completion via `POST /api/generate` with
`stream: false`. Returns `{model, response, done_reason, prompt_eval_count,
eval_count, total_duration_ms, load_duration_ms, tokens_per_second}`. Sampling
options are only forwarded when you set them, so omitting `temperature` and
`num_predict` leaves each model's own defaults intact rather than overriding
them with zeros. `format` accepts only `json`, which constrains the output to
valid JSON; any other value is rejected before a request is issued.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `model` | string | yes | — | Model name exactly as reported by `list_models`. |
| `prompt` | string | yes | — | The prompt to complete. |
| `system` | string | no | — | System prompt overriding the one baked into the model. |
| `temperature` | number | no | model default | Sampling temperature. |
| `num_predict` | number | no | model default | Maximum tokens to generate. |
| `format` | string | no | — | Only `json` is accepted; constrains output to valid JSON. |
| `timeout_seconds` | number | no | `300` | Per-call timeout. Max `900`. |

**`embed`** — an embedding vector via `POST /api/embeddings`. Returns `{model,
dimensions, embedding}`. Use an embedding model (`nomic-embed-text`,
`qwen3-embedding`, `mxbai-embed-large`); a chat model usually still answers, but
with vectors that are not meant for similarity search. Set `include_vector:
false` when you only need the dimensions — a few thousand floats is a heavy
payload to push through an agent's context.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `model` | string | yes | — | Embedding model as reported by `list_models`. |
| `prompt` | string | yes | — | Text to embed. |
| `include_vector` | boolean | no | `true` | Set `false` to return only `{model, dimensions}`. |
| `timeout_seconds` | number | no | `60` | Per-call timeout. Max `900`. |

**`model_info`** — metadata via `POST /api/show`. Returns `{model, family,
families, parameter_size, quantization, context_length, capabilities}`. The
context window is read from the architecture-namespaced key Ollama reports
(`qwen2.context_length`, `llama.context_length`, …), so it works across model
families; check it before sending a long prompt. The raw `modelfile`,
`template`, `parameters` and `license` fields are kilobytes of noise for an
agent and are omitted unless `verbose` is set.

| Argument | Type | Required | Default | Description |
|----------|------|:---:|---------|-------------|
| `model` | string | yes | — | Model name as reported by `list_models`. |
| `verbose` | boolean | no | `false` | Also return the raw modelfile, template, parameters and license. |
| `timeout_seconds` | number | no | `15` | Per-call timeout. Max `900`. |

---

## Recipes

**Read a large log in pages.** `read_file` refuses to buffer a file over
`DROIDMCP_MAX_READ_BYTES` in one shot. Page it: call with `offset: 0,
length: 1000000`, then `offset: 1000000`, and so on; or use `read_file_lines`
with a moving `start`/`end` window when you want line boundaries.

**Find then act.** Use `search_files` with a `pattern` like `*.md` (or a `regex`)
to locate files, then feed the returned relative paths straight back into
`read_file`, `move_file`, or `delete_file`.

**Scrape a JavaScript-populated table.** If `extract_table` returns nothing
because the table is injected after load, add `wait_selector: "table tbody tr"`
so the fetch retries until rows exist, then re-run the extraction.

**Open a PR end to end.** `commit_file` the change onto a branch, `create_pr`
from that `head` into `base`, optionally `review_pr` with `event: "APPROVE"`,
then `merge_pr` with `merge_method: "squash"`.

**Inventory the LAN.** Run `scan_network` once (auto-detects the subnet) to
populate the store, then `list_devices` to see everything and
`get_device_info` with an IP or MAC for a single host. `check_ports` deep-dives
one host's ports.

**Push a notification from an agent.** With `mcp-termux` running and Termux:API
installed, `termux_notification` (title/content) or `termux_toast` (text) surface
messages on the device; `termux_tts_speak` reads text aloud. For a managed flow —
updating or clearing a notification — use `mcp-notifications`: `send_notification`
with a stable `id`, call it again with the same `id` to update, `list_notifications`
to inspect what is showing, and `dismiss_notification` to clear it. Check
`get_dnd_status` first to avoid interrupting Do Not Disturb.

**Thumbnail a media folder.** `list_media` with `recursive: true` (optionally
`types: ["video"]`) enumerates the files; feed each returned `path` into
`thumbnail`, writing to a `thumbs/` destination and passing a `timestamp` to grab
a representative frame from videos. `get_metadata` gives you dimensions and EXIF
for any single file, and `convert_image` / `extract_audio` handle format changes.

**Keep local state in SQLite.** `open_db` a file under root, `execute` your
`CREATE TABLE`, then `execute` inserts with `?` placeholders and an `args` array
(never string-format values into the SQL). Read it back with `query`, inspect the
schema with `list_tables` / `describe_table`, and hand a snapshot to another tool
with `export_csv`.

---

## Troubleshooting

| Symptom | Cause and fix |
|---------|---------------|
| Server exits immediately, logs `requires DROIDMCP_ROOT` | `mcp-filesystem`, `mcp-media`, or `mcp-sqlite` was started without `DROIDMCP_ROOT`. Set it to a real directory. |
| Server exits, logs `requires DROIDMCP_..._KEY or DROIDMCP_API_KEY` | `mcp-filesystem`/`mcp-termux`/`mcp-media`/`mcp-sqlite`/`mcp-github`/`mcp-sms` need a key. Set one; they have no dev mode. |
| Clients get `403 forbidden host` | The request's `Host` header is not loopback. Connect via `localhost`/`127.0.0.1`/`::1`, or add the front-end hostname to `DROIDMCP_ALLOWED_HOSTS`. |
| Server exits, logs `DROIDMCP_PORT out of range` or `not a directory` | Config validation failed. Port must be `1`–`65535`; `DROIDMCP_ROOT` must exist and be a directory. |
| Clients get `401 unauthorized` | A key is configured but the client isn't sending `X-DroidMCP-Key`, or it doesn't match. `/healthz` is exempt, so a working health probe with failing tool calls points at the header. |
| `mcp-github` won't start, `token validation failed` | The token is missing, expired, or lacks scope. Set `GITHUB_TOKEN` (or `GITHUB_APP_TOKEN` / `GITHUB_FINE_GRAINED_TOKEN`). |
| Scraper returns `target is not allowed` / private-range error | SSRF protection blocked a private/loopback URL. Set `DROIDMCP_SCRAPER_ALLOW_PRIVATE=1` only if you trust the deployment. |
| Network tool returns `not in a private network range` | The target is public and `DROIDMCP_NETWORK_ALLOW_PUBLIC` is off. Set it to `1` to allow public targets. |
| `read_file` errors `file exceeds max read size` | The file is larger than `DROIDMCP_MAX_READ_BYTES`. Page it with `offset`/`length`, or raise the cap. |
| Clipboard/termux wrappers fail with a "termux-api not installed" hint | Install `pkg install termux-api` and the Termux:API app, then grant its permissions. |
| `run_command` says a command is `not in DROIDMCP_TERMUX_ALLOWLIST` | The allowlist is set and doesn't include that command. Add it, or clear the variable to allow all. |
| `mcp-media` transform fails with an `ffmpeg not found` hint | `convert_image`/`thumbnail`/`extract_audio` need ffmpeg. Run `pkg install ffmpeg`, or set `DROIDMCP_MEDIA_FFMPEG` to its path. |
| `mcp-sqlite` returns `database … does not exist; call open_db first` | Only `open_db` creates a database; `query`/`execute`/etc. require an existing file. Call `open_db` (or fix the path). |
| `mcp-sqlite` `query` returns `query only runs read statements` | A write statement was sent to `query`. Use `execute` for `INSERT`/`UPDATE`/`DELETE`/DDL. |
| `mcp-sensors` `get_brightness` errors with `cannot read screen brightness` | The device restricts the Android settings provider (Termux:API cannot read brightness at all). The other sensor tools are unaffected. |
| `mcp-sensors` `get_location` times out | A fresh GPS fix needs sky view and can exceed the timeout. Use `request: "last"` for the cached fix, `provider: "network"` for a coarse one, or raise `timeout_seconds`. |
| `mcp-notifications` `list_notifications` returns an empty array | Termux:API lacks the **Notification Access** permission. Grant it to Termux:API in Android's *Notification access* settings; posting and dismissing still work without it. |
| `mcp-notifications` `get_dnd_status` errors with `cannot read Do Not Disturb state` | The device restricts the Android settings provider (Termux:API has no DND getter). The other notification tools are unaffected. |
| `mcp-llm-proxy` exits, logs `cannot use the configured Ollama host` | `DROIDMCP_OLLAMA_HOST` is unparseable, uses a scheme other than http/https, or points outside the device/LAN. Fix the value, or set `DROIDMCP_LLMPROXY_ALLOW_REMOTE=1` if a remote daemon is intended. |
| `mcp-llm-proxy` tools return `cannot reach ollama at …` | The daemon is not running or listens elsewhere. Start it with `ollama serve`, check `curl http://127.0.0.1:11434/api/tags`, and point `DROIDMCP_OLLAMA_HOST` at it if the port differs. Under proot use `127.0.0.1`, not `localhost`. |
| `mcp-llm-proxy` `generate` returns `did not answer in time` | The model is too large for the timeout. Raise `timeout_seconds` (max `900`), lower `num_predict`, or use a smaller model. |
| Can't reach a server from another machine | By design: the listener is bound to `127.0.0.1`. Front it with a reverse proxy or port-forward, and read [`security.md`](security.md) before exposing it. |

For anything security-related — exposure, keys, TLS, the full threat model —
see [`security.md`](security.md).
