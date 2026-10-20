# Changelog

Notable changes to DroidMCP. Versions follow the git tags; each tagged release
ships one ARM64 binary per server plus a cosign-signed `SHA256SUMS`.

## [0.4.0] — Unreleased

### Added

- **Streamable HTTP transport** at `/mcp` on every server — the transport of the
  current MCP specification. The legacy HTTP+SSE endpoints (`/sse`, `/message`)
  keep working on the same port, behind the same key.
- **stdio transport** with `DROIDMCP_TRANSPORT=stdio`: the client launches the
  binary and talks to it over stdin/stdout, with no port, no listener and no
  key. Meant for Claude Code or Gemini CLI running in Termux on the device.
- `DROIDMCP_<SERVER>_DISABLED_TOOLS` on every server: the listed tools are
  removed before the server starts, so clients can neither list nor call them
  (e.g. `DROIDMCP_TERMUX_DISABLED_TOOLS=termux_sms_send`). An unknown name stops
  the server and lists the valid ones, so a typo cannot leave a tool enabled.
- `DROIDMCP_<SERVER>_RATE_LIMITS` on every server and transport: caps such as
  `send_sms=3/h` or `*=120/m` (the whole server) over a sliding window. A call
  over the limit is not run and returns an error result saying when to retry.
  An unknown tool name or bad syntax stops the server at startup.
- `SECURITY.md` with the reporting process and scope, and a
  [Verifying a release](docs/security.md#verifying-a-release) section with the
  exact `cosign verify-blob` command (the README already pointed there).
- CI: `govulncheck` gates releases alongside test, lint and gosec; every push and
  PR cross-compiles android/arm64; Dependabot proposes Go module and Actions
  updates weekly.
- Four servers that had not been in a release yet (v0.3.0 shipped ten):
  - `mcp-contacts` — read-only address book: search, details, JSON/vCard export.
  - `mcp-sms` — list, search and send SMS. No dev mode.
  - `mcp-llm-proxy` — on-device models through a local Ollama daemon.
  - `mcp-automation` — scheduled shell scripts (cron or interval) under a
    mandatory command allowlist. No dev mode.

### Security

- SMS sending is capped at 10 messages per hour by default (`send_sms` in
  `mcp-sms`, `termux_sms_send` in `mcp-termux`): the messages are billable and
  irreversible, so a runaway agent is stopped even when nothing was configured.
- The server warns at startup when the API key is shorter than 16 characters.
  Failed key attempts are deliberately not rate limited: every client is
  `127.0.0.1`, so a lockout would let any local app lock the real client out.
- `mcp-termux` no longer allows every command by default.
  `DROIDMCP_TERMUX_ALLOWLIST` is mandatory; allowing every program takes an
  explicit `*`, which logs a warning. No allowlist is needed when `run_command`
  is disabled.
- `mcp-termux`'s `termux_sms_send` validates recipients and passes the body on
  stdin, as `mcp-sms` does. A malformed "number" used to reach
  `termux-sms-send` as-is.
- `mcp-termux`'s `read_env` replaces the values of credential-looking variables
  (`GITHUB_TOKEN`, `DROIDMCP_*_KEY`, …) with `[REDACTED]`. It used to hand the
  server's own secrets to the calling model.
- `golang.org/x/net` v0.60.0, fixing five advisories in v0.59.0 (none reachable
  from DroidMCP's code, per govulncheck).
- CI pins third-party actions to commit SHAs and golangci-lint / gosec to exact
  versions; `gosec` used to run from its `master` branch.
- A browser `Origin` header, when present, must name a loopback host (or one in
  `DROIDMCP_ALLOWED_HOSTS`), as the Streamable HTTP specification requires.
- `filesystem`, `media` and `sqlite` refuse to start with `DROIDMCP_ROOT=/`.
- Path containment lives in one package, `internal/pathsec`, and now follows
  dangling symlinks: a link inside the root pointing outside it could let
  `write_file`, `copy_file`, ffmpeg output or `open_db` create files outside the
  root.
- `mcp-sqlite` rejects `ATTACH DATABASE` and `VACUUM … INTO` in `query`,
  `execute` and `export_csv`; both read or write a file path taken from the SQL.
- `mcp-media` rejects `%` in output names: ffmpeg's image2 muxer expanded `%d`
  and could overwrite other files.

### Fixed

- **Release binaries crashed in Termux** (`SIGSYS: bad system call`) on the
  first tool that runs a command, v0.3.0 included. They were built for
  `GOOS=linux`, whose `exec.LookPath` calls `faccessat2`, and Android's seccomp
  filter kills any app that does. Releases are now built for `GOOS=android`.
  Binaries built in Termux were not affected.
- **DNS failed in the release binaries**: Android has no `/etc/resolv.conf`, so
  Go asked `localhost:53`. Servers now fall back to Termux's
  `$PREFIX/etc/resolv.conf`, and `network_info` lists those servers.
- `mcp-automation` follows `TZ` in android builds too, where Go's `time.Local`
  is always UTC.
- `scan_network` without `subnet` failed on Android 11+, which hides network
  interfaces from apps. It now scans the `/24` around the Wi-Fi address from
  Termux:API, and says so in `note` (`network_info` too).
- `copy_file` truncated the source when copying a file onto itself, including
  through a hard link. Recursive copies into the source itself or one of its
  ancestors, through symlink aliases, onto FIFOs, or that left 0777 files behind
  are fixed too.
- `export_csv` writes to a temporary file, syncs it and renames it into place,
  so an error mid-query no longer replaces an existing file with a truncated CSV.
- `timeout_seconds`: very large values could overflow `time.Duration` in nine
  servers. One helper, `core.TimeoutArg`, now clamps before converting.
- SQLite: `float64` values outside the `int64` range are no longer bound as
  wrong integers.
- Scraper cache: hits respect the size limit and `wait_selector`, cached bodies
  share a 32 MiB budget, response headers are capped at 64 KiB, and
  `wait_interval_ms` is capped at 30 s.
- GitHub retries: a huge `Retry-After` is clamped before it is multiplied.
- `make install` copies into `$(PREFIX)/bin` (Termux sets `PREFIX`; override it
  elsewhere) instead of a hard-coded Termux path.
- Docs: fourteen servers, not thirteen; `mcp-scraper` runs on `net/http` +
  goquery (colly was dropped long ago, cobra never used); `mcp-media` and
  `mcp-sqlite` also require `DROIDMCP_ROOT`; every Termux:API server is listed
  as needing the `termux-api` package.
- Build: the build loops stop at the first error, and `SHA256SUMS` no longer
  lists the per-binary `.sha256` files that are deleted afterwards.

### Changed

- Every server has its own default port, `3000`–`3013` (the table in the
  README); all of them used to default to `3000`.
- Client setup docs corrected. Claude Code reads MCP servers from
  `~/.claude.json` and `.mcp.json` (written by `claude mcp add`), not from
  `~/.claude/settings.json`; Gemini CLI uses `httpUrl` (Streamable HTTP) or `url`
  (SSE), not `uri`.
- Internal: `core.DroidServer.ServeSSE(port)` became `Serve(cfg)`, which picks
  the transport, and `config.LoadConfig()` takes the server name.

### Upgrading

- **mcp-termux will not start** until `DROIDMCP_TERMUX_ALLOWLIST` is set. List
  the programs `run_command` needs, or set it to `*` to keep the old
  allow-everything behavior.
- **SMS sending is capped at 10 per hour.** For more, set e.g.
  `DROIDMCP_SMS_RATE_LIMITS=send_sms=60/h` (or `DROIDMCP_TERMUX_RATE_LIMITS`
  for `termux_sms_send`), or `=off`.
- **Default ports moved.** A server other than `mcp-filesystem` started without
  `DROIDMCP_PORT` now listens on its own port (e.g. `mcp-github` on `3001`), not
  on `3000`. Update the client URL or set `DROIDMCP_PORT`.
- Point clients at `http://localhost:<port>/mcp`. `/sse` still works.
- If you configured Claude Code from the previous README snippet, register the
  server again with `claude mcp add` (see the
  [usage guide](docs/usage.md#talking-to-a-server)).

## Earlier releases

[v0.3.0](https://github.com/kahz12/DroidMCP/releases/tag/v0.3.0),
[v0.2.0](https://github.com/kahz12/DroidMCP/releases/tag/v0.2.0) and
[v0.1.0](https://github.com/kahz12/DroidMCP/releases/tag/v0.1.0) are described
on their GitHub release pages.

[0.4.0]: https://github.com/kahz12/DroidMCP/compare/v0.3.0...v0.4.0
