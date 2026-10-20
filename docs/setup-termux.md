# Termux Setup Guide for DroidMCP

This guide walks you through setting up and running DroidMCP servers on
an Android device using Termux. For the threat model, authentication
options, TLS and the production checklist, see
[`security.md`](security.md).

## Prerequisites

1. Install **Termux** (preferably from
   [F-Droid](https://f-droid.org/en/packages/com.termux/) — the Play Store
   build is outdated and may not work).
2. Update the package list:

   ```bash
   pkg update && pkg upgrade
   ```

## Install dependencies

DroidMCP itself only needs Go and Make to build:

```bash
pkg install golang git make
```

The servers built on Termux:API — `mcp-clipboard`, `mcp-sensors`,
`mcp-notifications`, `mcp-contacts`, `mcp-sms`, and the wrapper tools of
`mcp-termux` (battery, location, notifications, toast, sms, tts) —
additionally require the `termux-api` package **and** the companion
Android app.

```bash
# 1. In Termux:
pkg install termux-api

# 2. On the device:
#    Install the "Termux:API" app from the same source as Termux
#    (F-Droid is recommended). Open it once and grant any permission
#    prompts (clipboard access on newer Android, location, SMS, etc.).
```

Without these, `mcp-clipboard` returns:

```
termux-api package not installed; run `pkg install termux-api` and
ensure the Termux:API app is installed on the device
```

…and the termux wrappers (e.g. `termux-battery-status`) fail with a
similar error from the OS shell.

## Building the servers

```bash
git clone https://github.com/kahz12/DroidMCP
cd DroidMCP
make build
```

Binaries land in `bin/`. The same flags are used by CI, so a local
build of the same commit is byte-identical to the released artifact.
A `bin/SHA256SUMS` is produced alongside if you use
`make checksums` or `./scripts/build-arm64.sh`.

## Configuring environment variables

The full table lives in the top-level [`README`](../README.md#configuration)
and the operational notes are in [`security.md`](security.md). The
minimum you should set:

| Variable | Why |
|----------|-----|
| `DROIDMCP_PORT` | Optional: every server has its own default port (`3000`–`3013`); set it only to move one. |
| `DROIDMCP_ROOT` | The default `/` is **insecure** — override to a real directory. `filesystem`, `media` and `sqlite` refuse to start with `/`. |
| `DROIDMCP_API_KEY` (or per-server) | Required as soon as the listener is reachable from anything but `localhost`. |
| `GITHUB_TOKEN` | Required to run `mcp-github`. |

## Running the servers

Servers run in the foreground and write logs to stderr. In Termux, the
ergonomic options are:

- **`tmux`** — run each server in its own pane:

  ```bash
  pkg install tmux
  tmux new -s droidmcp
  # Ctrl+B " to split, Ctrl+B C to open a new window per server
  ```

- **Termux:Boot** — auto-start on device boot. Place a script in
  `~/.termux/boot/` that exports the env vars and execs the binary.

Example, filesystem server bound to your Documents folder:

```bash
export DROIDMCP_PORT=3000
export DROIDMCP_ROOT=/storage/emulated/0/Documents
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"

./bin/droidmcp-filesystem
```

Verify it is alive from another shell:

```bash
curl -fsS http://localhost:3000/healthz
# {"status":"ok","server":"mcp-filesystem","version":"dev"}
```

(Health probes bypass auth by design — see [`security.md`](security.md).)

## Connecting MCP clients

The endpoint each client should hit is `http://localhost:<port>/mcp`
(Streamable HTTP; `https://` once you configure `DROIDMCP_TLS_CERT` /
`_KEY`). Clients that only speak the older SSE transport use `/sse` on
the same port. When auth is enabled, clients must send the key in the
`X-DroidMCP-Key` header:

```bash
claude mcp add --transport http --scope user filesystem http://localhost:3000/mcp \
  --header "X-DroidMCP-Key: $DROIDMCP_API_KEY"
```

If the client itself runs in Termux, stdio is simpler: no port, no key
and no server to keep running — the client launches the binary.

```bash
claude mcp add --env DROIDMCP_TRANSPORT=stdio --env DROIDMCP_ROOT="$HOME/workspace" \
  --transport stdio --scope user filesystem -- droidmcp-filesystem
```

Gemini CLI configuration and the project-scoped `.mcp.json` form are in
the [usage guide](usage.md#talking-to-a-server).

## Security

DroidMCP servers always listen on the loopback interface. The
low-privilege ones default to **dev mode** (no API key) for ergonomics;
`filesystem`, `termux`, `media`, `sqlite`, `github`, `sms` and
`automation` refuse to listen without a key. The moment the listener can
be reached from anything other than the local device, you need:

- a strong `DROIDMCP_API_KEY` (or per-server `DROIDMCP_<NAME>_KEY`),
- TLS via `DROIDMCP_TLS_CERT` / `DROIDMCP_TLS_KEY`,
- an explicit `DROIDMCP_ROOT` for `mcp-filesystem`, `mcp-media` and
  `mcp-sqlite` (not `/` — they refuse it),
- and a `DROIDMCP_TERMUX_ALLOWLIST` narrower than `*` if you run
  `mcp-termux` (it is mandatory either way).

The full checklist is in [`security.md`](security.md#production-checklist).
**Never expose these ports to external networks without working through
that checklist first** — `mcp-termux` and `mcp-filesystem` in particular
hand the caller a lot of authority over your device.
