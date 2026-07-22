# Security & Operations Guide

This document describes the threat model assumed by DroidMCP, the
difference between **dev mode** and **production mode**, and the
configuration knobs each operator should set before exposing a server
beyond a single shell session.

If you only read one section: never expose a DroidMCP port outside
`localhost` without **both** a strong `DROIDMCP_API_KEY` *and* TLS
(`DROIDMCP_TLS_CERT` / `DROIDMCP_TLS_KEY`). The `termux` and
`filesystem` servers in particular give whoever can reach the port the
ability to run arbitrary commands and read/write files.

## Threat model

DroidMCP assumes the following adversary capabilities and limits:

- **In scope.** Any process able to reach the listening TCP port (a
  malicious app on the same device, a peer on the same Wi-Fi if the
  port is bound to a non-loopback address, a captured client token).
- **In scope.** A misconfigured `DROIDMCP_ROOT` that points to a
  sensitive subtree (private keys, `~/.ssh`, the whole rootfs).
- **In scope.** A leaked API key in logs or error messages.
- **Out of scope.** A local attacker who already has root or the
  Termux UID, or who can attach a debugger. DroidMCP cannot defend
  against an attacker with the same privileges as the server.
- **Out of scope.** Arbitrary command execution as a *feature* of the
  `termux` MCP. That server is intentionally powerful and should be
  treated as a remote shell.

Mitigations the codebase currently implements:

| Layer | Mitigation |
|-------|------------|
| Auth | Per-server / global API key checked with `crypto/subtle.ConstantTimeCompare`. `mcp-filesystem`, `mcp-termux`, `mcp-media`, `mcp-sqlite` and `mcp-github` refuse to start without one. |
| Transport | Optional TLS via `DROIDMCP_TLS_CERT` / `_KEY`; HSTS sent only when TLS is active. |
| Host binding | Listener bound to `127.0.0.1`; every request must also present a loopback `Host` header (or one listed in `DROIDMCP_ALLOWED_HOSTS`), so a DNS-rebinding browser cannot drive the dev-mode servers. |
| Headers | `Cache-Control: no-store` and `X-Content-Type-Options: nosniff` on every response. |
| Logging | `slog`-based, with credential redaction in attribute keys (`api_key`, `token`, `password`, …). |
| `mcp-filesystem` | Requires an explicit `DROIDMCP_ROOT` and an API key. `securePath` rejects absolute paths and `..` traversal, then resolves symlinks and re-checks so a symlink under the root cannot point outside it. |
| `mcp-scraper` | Anti-SSRF: rejects RFC1918 / loopback / link-local by default (override with `DROIDMCP_SCRAPER_ALLOW_PRIVATE=1`), validated at the URL, on every redirect, and on the concrete resolved IP the socket dials (closes DNS rebinding). |
| `mcp-termux` `run_command` | `env_extra` rejects dynamic-linker overrides (`LD_*`, `DYLD_*`) so a caller cannot `LD_PRELOAD` past the command allowlist. |
| `mcp-network` | Refuses public targets by default (override with `DROIDMCP_NETWORK_ALLOW_PUBLIC=1`). |
| `mcp-termux` | Optional allowlist via `DROIDMCP_TERMUX_ALLOWLIST=cmd1,cmd2,…`; `install_pkg` quotes the package name (`pkg install -- <name>`). |
| `mcp-clipboard` | All inputs piped via stdin, never embedded in shell arguments. |
| `mcp-sqlite` | Requires `DROIDMCP_ROOT` + an API key; values bind as `?` parameters; `describe_table` validates the table name against the schema before quoting it; the read tools (`query`, `list_tables`, `describe_table`, `export_csv`) run on a `mode=ro` connection so the engine rejects any write, even one stacked after a `SELECT` or fronted by a CTE. |
| `mcp-contacts` | Read-only. The `termux-contact-list` backend takes no arguments; every filter (`query`, `name`, `number`) is applied in memory, so no caller-supplied text reaches a command line — there is no argument-injection surface. Dev mode is allowed on loopback, but a key is recommended because the address book is personal data. |

Known gaps that operators should keep in mind (tracked in `AUDIT_REPORT.txt`):

- `securePath` now resolves symlinks and re-checks containment (audit
  item 2.2 closed), but the check is not fully TOCTOU-proof: a process
  that can swap a symlink *inside the root* between the check and the
  operation could still race it. Don't mount a root that other
  untrusted processes can write to.
- No rate limit yet (audit 2.7). Pair the server with a reverse proxy
  if you need one.

## Authentication

Every server enforces the same scheme:

1. On startup the server resolves an API key via
   `config.ResolveAPIKey("<server-name>")` which checks, in order:
   - `DROIDMCP_<SERVER>_KEY` (e.g. `DROIDMCP_TERMUX_KEY`)
   - `DROIDMCP_API_KEY` (global fallback)
2. If both are unset, the read-only / low-privilege servers start in
   **dev mode** and log `auth=disabled`. Every request is accepted. Use
   this only on loopback for local development. `mcp-filesystem`,
   `mcp-termux`, `mcp-media`, `mcp-sqlite` and `mcp-github` are the
   exceptions: they refuse to start without a key, because they expose
   command execution, read/write filesystem/database access, or a
   GitHub token that can read private repos and push commits.
3. If a key is set, every inbound request must carry it in the
   `X-DroidMCP-Key` HTTP header. The comparison is constant-time.
4. Independently of the key, every request's `Host` header must name a
   loopback destination (`localhost`, `127.0.0.1`, `::1`) or a hostname
   listed in `DROIDMCP_ALLOWED_HOSTS` (comma-separated, no port — set it
   when fronting the server with a reverse proxy or port-forward).
   Requests with any other `Host` get `403`, which stops a malicious web
   page from reaching a dev-mode server via DNS rebinding.
5. `GET /healthz` is served without the API key so external supervisors
   (systemd, k8s, docker healthchecks) can probe the server; the loopback
   `Host` check still applies.

Per-server keys override the global one, so you can give a different
client a different key per MCP:

```bash
# Global key used by everything except termux.
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"
# Stricter key only for the high-privilege shell server.
export DROIDMCP_TERMUX_KEY="$(openssl rand -base64 32)"
```

Clients pass the key as a header. Example with `curl`:

```bash
curl -H "X-DroidMCP-Key: $DROIDMCP_API_KEY" http://localhost:3000/sse
```

For Claude Code / Gemini CLI, set the header in the MCP server entry:

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "sse",
      "url": "https://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<paste-the-same-value>" }
    }
  }
}
```

## TLS

Plain HTTP is fine on `localhost`. Anywhere else, terminate TLS at the
server:

```bash
export DROIDMCP_TLS_CERT=/path/to/cert.pem
export DROIDMCP_TLS_KEY=/path/to/key.pem
droidmcp-filesystem
```

When both env vars are present:

- `baseURL` advertised in the MCP handshake becomes `https://…`.
- `ListenAndServeTLS` is used instead of `ListenAndServe`.
- `Strict-Transport-Security: max-age=31536000; includeSubDomains` is
  added to every response. (Sent only over TLS — advertising HSTS over
  plain HTTP would lock browsers out.)

You can self-sign with `openssl req -x509 …` for an internal device,
but for any public exposure use a real certificate.

## Logging

DroidMCP writes structured logs to **stderr** (stdout is reserved for
potential protocol traffic). Two env vars control behaviour:

| Variable | Values | Default |
|----------|--------|---------|
| `DROIDMCP_LOG_LEVEL` | `debug`, `info`, `warn`/`warning`, `error`/`err` | `info` |
| `DROIDMCP_LOG_FORMAT` | `json`, anything else falls back to `text` | `text` |

In production, prefer JSON so the logs are machine-parseable:

```bash
export DROIDMCP_LOG_LEVEL=info
export DROIDMCP_LOG_FORMAT=json
```

**Credential redaction.** Attribute keys whose names match
`token`, `secret`, `password`, `passwd`, `authorization`, `apikey`,
`api_key`, `api-key`, or the standalone word `key` are replaced with
`[REDACTED]` before they reach the sink. The redactor is intentionally
narrow to avoid mangling normal attributes like `auth=enabled` in the
startup banner — see `internal/logger/logger.go` for the full list.

The request logger never reads or logs the `X-DroidMCP-Key` header.

## Filesystem root

`mcp-filesystem` confines all paths to `DROIDMCP_ROOT`. It **requires
`DROIDMCP_ROOT` to be set explicitly** and refuses to start otherwise —
the shared config default of `/` is never used to grant access, so an
unconfigured server cannot silently expose the whole device.

Set the root (and an API key — the server also refuses to start
without one):

```bash
# On Android / Termux:
export DROIDMCP_ROOT=/storage/emulated/0/DroidMCP

# On a Linux box:
export DROIDMCP_ROOT=/srv/droidmcp/workspace

export DROIDMCP_FILESYSTEM_KEY="$(openssl rand -base64 32)"  # or DROIDMCP_API_KEY
```

The directory must exist and be a directory; startup fail-fasts with a
descriptive error otherwise (`DROIDMCP_ROOT "<path>": not a
directory`). `securePath` resolves symlinks and re-verifies containment,
so a symlink under the root pointing elsewhere is rejected rather than
followed (audit item 2.2). The resolution is not fully TOCTOU-proof, so
still avoid mounting a root other untrusted processes can write to.

## `mcp-clipboard` requirements

The clipboard server shells out to `termux-clipboard-get` and
`termux-clipboard-set`, both of which come from the
[`termux-api`](https://wiki.termux.com/wiki/Termux:API) package.
Without it, every clipboard request returns:

```
termux-api package not installed; run `pkg install termux-api` and
ensure the Termux:API app is installed on the device
```

To make the server usable:

1. Install the `Termux:API` Android app (F-Droid or Play Store) — the
   same source as Termux itself.
2. In the Termux shell:

   ```bash
   pkg install termux-api
   ```

3. Grant the Termux:API app any permissions it requests on first run
   (clipboard access on newer Android versions).

The clipboard server also stores a local history in memory (no disk
persistence). Two env vars cap it:

| Variable | Meaning | Default |
|----------|---------|---------|
| `DROIDMCP_CLIPBOARD_HISTORY_ENTRIES` | Max entries kept | server-defined |
| `DROIDMCP_CLIPBOARD_HISTORY_BYTES`   | Max total bytes  | server-defined |

Older entries are evicted FIFO when either cap is reached.

## `mcp-termux` allowlist

`mcp-termux` exposes `run_command`, which is effectively a remote
shell. Three safeguards exist:

- `install_pkg` quotes the package name (`pkg install -- <name>`) so
  flags injected via the package field cannot reach `pkg`.
- `DROIDMCP_TERMUX_ALLOWLIST` restricts which top-level commands are
  callable through `run_command`:

  ```bash
  export DROIDMCP_TERMUX_ALLOWLIST="ls,cat,grep,git,go"
  ```

  When unset, every command is allowed. The wrappers (`termux-battery-status`,
  `termux-location`, `termux-notification`, `termux-toast`,
  `termux-sms-send`, `termux-tts-speak`) bypass the allowlist on
  purpose — those are explicit tools and the operator opted in by
  starting the server.
- `run_command`'s `env_extra` refuses dynamic-linker overrides
  (`LD_*`, `DYLD_*`): otherwise a caller could `LD_PRELOAD` arbitrary
  code into an allowlisted, benign binary and sidestep the allowlist.
  An operator that genuinely needs such a variable can set it in the
  server's own environment, which the child still inherits.

Even so, keep in mind the allowlist is a coarse control: many
legitimately-allowlisted programs (`sh`, `python`, `find`, `git`) can
themselves run other code, so treat the allowlist as reducing blast
radius, not as a strict sandbox.

If you do not need shell access, do not start `droidmcp-termux`.

## Scraper and network defaults

`mcp-scraper` rejects RFC1918 / link-local / loopback URLs by default
to prevent SSRF (audit 2.1). Override on a hardened, isolated host
only:

```bash
export DROIDMCP_SCRAPER_ALLOW_PRIVATE=1
```

`mcp-network` refuses non-RFC1918 targets by default to prevent
turning the device into a port scanner against the public internet
(audit 2.10). Same override pattern:

```bash
export DROIDMCP_NETWORK_ALLOW_PUBLIC=1
```

Both knobs accept `1`, `true`, `yes`, `on` (case-insensitive).

`mcp-network` also keeps a persistent inventory of every host seen by
`scan_network` (IP, MAC, open ports, first/last seen) so `list_devices`
and `get_device_info` can answer without re-scanning. It is written to
`~/.droidmcp/network-devices.json` (override with `DROIDMCP_NETWORK_DB`)
with `0600`/`0700` permissions. Treat that file as sensitive — it is a
map of the local network — and point `DROIDMCP_NETWORK_DB` at a path only
the service user can read when running multi-tenant.

## Production checklist

Before exposing any DroidMCP server beyond `localhost`:

- [ ] `DROIDMCP_API_KEY` set to a random ≥32-byte secret (or a
      per-server key for every server you start).
- [ ] `DROIDMCP_TLS_CERT` / `_KEY` configured if the listener is
      reachable from anything but loopback.
- [ ] `DROIDMCP_ROOT` set to a dedicated directory (required by
      `mcp-filesystem`, which won't start without it) — never `/`.
- [ ] `DROIDMCP_LOG_FORMAT=json` and the logs shipped somewhere
      durable.
- [ ] `DROIDMCP_TERMUX_ALLOWLIST` set if you actually need
      `mcp-termux`. Otherwise don't run it.
- [ ] `DROIDMCP_SCRAPER_ALLOW_PRIVATE` / `_NETWORK_ALLOW_PUBLIC`
      left unset unless you understand the implications.
- [ ] `GET /healthz` returns 200 from outside (e.g. a smoke check
      from your supervisor).
- [ ] Binary verified against the published `SHA256SUMS` (and ideally
      the cosign `.sig`/`.pem` for the release tag).

In dev mode (loopback only, no key, plain HTTP) the scraper, network
and clipboard servers are fine for experimentation — just understand
the moment you bind to a non-loopback interface, you owe yourself the
items above. `mcp-termux` and `mcp-filesystem` have no dev mode: both
require a key (and filesystem also requires `DROIDMCP_ROOT`) even on
loopback.
