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
| Auth | Per-server / global API key checked with `crypto/subtle.ConstantTimeCompare`. `mcp-filesystem`, `mcp-termux`, `mcp-media`, `mcp-sqlite`, `mcp-github`, `mcp-sms` and `mcp-automation` refuse to start without one. |
| Transport | Optional TLS via `DROIDMCP_TLS_CERT` / `_KEY`; HSTS sent only when TLS is active. |
| Host binding | Listener bound to `127.0.0.1`; every request must also present a loopback `Host` header (or one listed in `DROIDMCP_ALLOWED_HOSTS`), so a DNS-rebinding browser cannot drive the dev-mode servers. |
| Headers | `Cache-Control: no-store` and `X-Content-Type-Options: nosniff` on every response. |
| Logging | `slog`-based, with credential redaction in attribute keys (`api_key`, `token`, `password`, …). |
| `mcp-filesystem` | Requires an explicit `DROIDMCP_ROOT` and an API key. `securePath` rejects absolute paths and `..` traversal, then resolves symlinks and re-checks so a symlink under the root cannot point outside it — including a dangling symlink, whose not-yet-existing target would otherwise be created outside the root by a write. A `DROIDMCP_ROOT` of `/` is refused at startup (filesystem, media and sqlite). `copy_file` compares real locations, so a directory cannot be copied into or over itself through a symlink alias. |
| `mcp-scraper` | Anti-SSRF: rejects RFC1918 / loopback / link-local by default (override with `DROIDMCP_SCRAPER_ALLOW_PRIVATE=1`), validated at the URL, on every redirect, and on the concrete resolved IP the socket dials (closes DNS rebinding). |
| `mcp-termux` `run_command` | `env_extra` rejects dynamic-linker overrides (`LD_*`, `DYLD_*`) so a caller cannot `LD_PRELOAD` past the command allowlist. |
| `mcp-network` | Refuses public targets by default (override with `DROIDMCP_NETWORK_ALLOW_PUBLIC=1`). |
| `mcp-termux` | Optional allowlist via `DROIDMCP_TERMUX_ALLOWLIST=cmd1,cmd2,…`; `install_pkg` quotes the package name (`pkg install -- <name>`). |
| `mcp-clipboard` | All inputs piped via stdin, never embedded in shell arguments. |
| `mcp-sqlite` | Requires `DROIDMCP_ROOT` + an API key; values bind as `?` parameters; `describe_table` validates the table name against the schema before quoting it; the read tools (`query`, `list_tables`, `describe_table`, `export_csv`) run on a `mode=ro` connection so the engine rejects any write, even one stacked after a `SELECT` or fronted by a CTE. `ATTACH DATABASE` and `VACUUM … INTO` are rejected by every SQL tool (`query`, `execute`, `export_csv`): they take a file path from the SQL text and would read or write files outside the root. |
| `mcp-contacts` | Read-only. The `termux-contact-list` backend takes no arguments; every filter (`query`, `name`, `number`) is applied in memory, so no caller-supplied text reaches a command line — there is no argument-injection surface. Dev mode is allowed on loopback, but a key is recommended because the address book is personal data. |
| `mcp-sms` | Highest-privilege Termux:API server — no dev mode (refuses to start unkeyed), since reading exposes OTP/2FA codes and `send_sms` dispatches a real, billable, irreversible message. `send_sms` recipients are validated against `^\+?[0-9]{3,}$` and passed as a single argv element; the body is delivered on **stdin**, never as an argument, so message content cannot be parsed as an option or reach a shell. `list_sms`/`search_sms` build argv only from a validated `type` enum and integers; search filtering is in-memory. |
| `mcp-llm-proxy` | The inverse of the scraper's problem: the destination is fixed by the operator (`DROIDMCP_OLLAMA_HOST`) and is never taken from a tool argument, so a calling model cannot redirect its own prompts. The resolved address must be loopback / RFC1918 / link-local / CGNAT or the server refuses to start; sending prompts to a public host takes an explicit `DROIDMCP_LLMPROXY_ALLOW_REMOTE=1`. That policy is enforced three times, since a single startup check is not enough: on the configured address, again on the concrete post-resolution IP at dial time (`net.Dialer.Control`, closing the re-resolution window for a hostname), and by refusing every redirect (Go replays the request body on a 307, so a `Location` header would otherwise forward the prompt verbatim to an unvetted host). The transport also ignores `HTTP_PROXY` so no proxy can divert traffic past those checks, responses are capped at 32 MiB, and no subprocess is ever spawned. Dev mode is allowed: it reads no device data and writes nothing. |
| `mcp-automation` | Unattended, scheduled command execution, so no dev mode and no allow-all default: it refuses to start without a key and without `DROIDMCP_AUTOMATION_ALLOWLIST`. Scripts run in an embedded shell interpreter (mvdan.cc/sh), not the system `sh`, and every external command — in pipes, `$(…)`, `<(…)`, background jobs, `eval`, sourced files — is checked against the allowlist when it starts; a refusal stops the run. Allowlisted names resolve through the server's `PATH`, not the script's; absolute paths must be listed exactly. Scripts cannot set `LD_*`/`DYLD_*` (inherited values pass unchanged) nor write files through redirections (only `/dev/null`). Runs have a timeout, capped output, and SIGTERM to their process group (then SIGKILL to the command) on timeout, task deletion or shutdown. See [below](#mcp-automation-scheduled-scripts). |

Known gaps that operators should keep in mind:

- `securePath` resolves symlinks and re-checks containment, but the
  check is not fully TOCTOU-proof: a process that can swap a symlink
  *inside the root* between the check and the operation could still
  race it. Don't mount a root that other untrusted processes can write
  to.
- No rate limit yet. Pair the server with a reverse proxy if you need
  one.

## Authentication

Every server enforces the same scheme:

1. On startup the server resolves an API key via
   `config.ResolveAPIKey("<server-name>")` which checks, in order:
   - `DROIDMCP_<SERVER>_KEY` (e.g. `DROIDMCP_TERMUX_KEY`)
   - `DROIDMCP_API_KEY` (global fallback)
2. If both are unset, the read-only / low-privilege servers start in
   **dev mode** and log `auth=disabled`. Every request is accepted. Use
   this only on loopback for local development. `mcp-filesystem`,
   `mcp-termux`, `mcp-media`, `mcp-sqlite`, `mcp-github`, `mcp-sms` and
   `mcp-automation` are the exceptions: they refuse to start without a
   key, because they expose command execution (immediate or scheduled),
   read/write filesystem/database access, a GitHub token that can read
   private repos and push commits, or SMS send plus OTP/2FA message
   contents.
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
followed. The resolution is not fully TOCTOU-proof, so
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

## `mcp-automation`: scheduled scripts

A task is a script that runs later with nobody watching, which makes a
loose policy worse than in `mcp-termux`: a mistake repeats on every
run, and a task can be planted to fire long after the session that
created it. The server is therefore stricter by construction.

- **The allowlist is mandatory.** The server refuses to start without
  `DROIDMCP_AUTOMATION_ALLOWLIST`, and an empty or blank value counts as
  missing. Entries are command names or absolute paths; a relative path
  such as `bin/tool` is a startup error.

  ```bash
  export DROIDMCP_AUTOMATION_KEY="$(openssl rand -base64 32)"
  export DROIDMCP_AUTOMATION_ALLOWLIST="termux-battery-status,termux-notification,jq"
  ```

- **The allowlist sees every command.** Scripts are not passed to
  `sh -c`, where the allowlist would only ever see `sh`. An embedded
  interpreter ([mvdan.cc/sh](https://github.com/mvdan/sh)) parses and
  runs the script and hands each external command to the server as it
  is about to start, wherever it appears: a pipeline, `$(…)`, `<(…)`, a
  background job, `eval`, `command`, `exec`, a sourced file. A command
  outside the list stops the run and is recorded in the task history.
  `create_task` also rejects scripts that name such a command literally,
  so most mistakes surface at creation time.
- **Names resolve through the server's `PATH`.** A script may set its
  own `PATH`, but an allowlisted name is always looked up with the
  server's, so `PATH=/tmp/evil:$PATH; jq` still runs the real `jq`. A
  command given by path runs only if that exact absolute path is listed.
- **No linker overrides.** If a command would start with an `LD_*` or
  `DYLD_*` variable that differs from the server's own environment, the
  run is refused; unsetting one is allowed. Termux's own `LD_PRELOAD`
  for `termux-exec` is inherited unchanged.
- **No file writes through the shell.** Redirections that open a file
  for writing (`>`, `>>`, `&>`, `>|`, `<>`) are refused except into
  `/dev/null`. A redirection is not a command, so the allowlist cannot
  see it, and `echo … >> ~/.bashrc` or a file in `~/.termux/boot/` would
  run arbitrary code later. Writing requires an allowlisted command.
- **Bounded runs.** Per-run timeout (max 30 minutes), 256 KiB of
  captured output per stream, at most 50 tasks, and the newest 20 runs
  kept per task. On timeout, task deletion or server shutdown the run's
  process group gets SIGTERM, and a command still running 2 seconds later
  gets SIGKILL. A grandchild that ignores SIGTERM and leaves the group can
  survive; the same is true of `mcp-termux`.
- **The store is a persistence point.** Tasks live in
  `DROIDMCP_AUTOMATION_DB` (mode `0600`, directory `0700`). Anyone who
  can write that file can add tasks, though the allowlist still applies
  when they run. A corrupt file stops the server instead of being
  silently replaced. `env_extra` values are stored there in plain text
  and returned by `list_tasks`, so do not put secrets in them.

What this does not protect against:

- **Allowlisted programs that run other code.** `sh`, `bash`, `python`,
  `find -exec`, `xargs`, `git` hooks, `awk 'BEGIN{system(…)}'`: listing
  one of them grants everything it can start, and those processes are
  outside the interpreter. Keep the list to the programs your tasks need.
- **Resource exhaustion inside the interpreter.** Builtins run in the
  server process. A script that grows a variable without bound can
  exhaust the server's memory before its timeout fires; the server is a
  single process, so that stops every task.
- **Shell compatibility.** The interpreter follows POSIX and bash
  syntax, not every quirk of Termux's `dash`. Test a task with
  `run_task` before relying on its schedule.

## Scraper and network defaults

`mcp-scraper` rejects RFC1918 / link-local / loopback URLs by default
to prevent SSRF. Override on a hardened, isolated host only:

```bash
export DROIDMCP_SCRAPER_ALLOW_PRIVATE=1
```

`mcp-network` refuses non-RFC1918 targets by default to prevent
turning the device into a port scanner against the public internet.
Same override pattern:

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
items above. `mcp-termux`, `mcp-filesystem`, `mcp-media`, `mcp-sqlite`,
`mcp-github`, `mcp-sms` and `mcp-automation` have no dev mode: they require a
key (filesystem/media/sqlite also require `DROIDMCP_ROOT`, and automation its
allowlist) even on loopback.
`mcp-sms` especially: it can send real messages and read OTP/2FA codes.
