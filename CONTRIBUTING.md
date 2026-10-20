# Contributing to DroidMCP

Thanks for your interest in contributing! DroidMCP aims to be the simplest way
to give AI agents native capabilities on Android: single Go binaries, zero
runtime dependencies, safe defaults. Contributions that keep that spirit are
very welcome.

## Ground rules

- **No runtime dependencies.** Servers must work as a standalone ARM64 binary
  on Termux. Pure-Go libraries are fine; anything that requires CGO, Node,
  Python, or a system daemon is not.
- **Safe by default.** New capabilities ship locked down (API key required,
  private-network guards, allowlists) and are opened up explicitly via
  `DROIDMCP_*` environment variables — never the other way around.
- **Every tool gets tests.** Handlers are plain functions taking
  `mcp.CallToolRequest`; test them directly (see any `*_test.go` under `cmd/`).
- **Never commit binaries.** `bin/` and root-level build output are
  gitignored; keep it that way.

## Development setup

On Termux (or any Linux/macOS box with the Go version declared in `go.mod`):

```bash
pkg install golang git make   # Termux; use your package manager elsewhere
git clone https://github.com/kahz12/DroidMCP
cd DroidMCP
make build                    # binaries in bin/
make test                     # go test -race ./...
```

Useful targets: `make vet`, `make fmt` / `make fmt-check`, `make lint`
(golangci-lint, config in `.golangci.yml`), `make sec` (gosec),
`make build-arm64` (cross-compile for `android/arm64` via
`scripts/build-arm64.sh`).

CI runs **test (plus an android/arm64 cross-build and vet) + lint + gosec +
govulncheck** on
every push and PR; all four must be green. Run `go run
golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` locally for the last one.

## Testing on a device

CI runs the tests on Linux, which hides a class of Android-only failures:
the app seccomp filter, no `/etc/resolv.conf`, Termux's `umask 077`, a
`/bin/sh` (mksh) without a `printf` builtin, and Android 11+ blocking
netlink and `/proc/net/route` for apps. When a change touches processes,
the network, files or time zones, run the tests on a phone too: build
them with `CGO_ENABLED=0 GOOS=android GOARCH=arm64 go test -c -o
test-<pkg> ./<pkg>` and run the binaries **inside Termux** (for example
over Termux's `sshd`), not through `adb shell`. `adb shell` runs as
another user, outside the app sandbox, so it skips most of the above.
`internal/config`'s port test reads `../../cmd`, so run that binary from
a directory laid out like the repository.

## Repository layout

```
cmd/<server>/     one MCP server per directory (main.go registers tools)
internal/core     shared server wrapper: Streamable HTTP + SSE + stdio (auth, TLS, /healthz)
internal/config   DROIDMCP_* environment configuration, per-server default ports
internal/logger   structured logging with secret redaction
internal/pathsec  containment of caller paths in DROIDMCP_ROOT
internal/phone    SMS recipient validation (mcp-sms, mcp-termux)
internal/dnsconf  DNS via Termux's resolv.conf where Android has none
internal/buildinfo  version stamped at build time
```

## Adding a tool to an existing server

1. Register it in `registerTools()` in `cmd/<server>/main.go` with
   `mcp.NewTool(...)` — clear description, typed parameters, sane defaults
   and caps for numeric knobs.
2. Implement the handler: return user-facing failures via
   `mcp.NewToolResultError(...)` (never a Go error for expected failures) and
   marshal successful responses to JSON.
3. Add unit tests covering the happy path and the error paths.
4. Update the tool tables in `README.md`, `README.es.md`, and `ROADMAP.md`,
   the tool reference in `docs/usage.md` and `docs/usage.es.md`, and add a line
   to `CHANGELOG.md`.

## Adding a new server

1. Create `cmd/<name>/main.go` following an existing server:
   `core.NewDroidServer("mcp-<name>", buildinfo.Version)`, resolve the API key
   with `config.ResolveAPIKey("<name>")`, register tools, `server.Serve(cfg)`
   (it picks HTTP or stdio from `DROIDMCP_TRANSPORT`).
2. Give it a default port in `defaultPorts` (`internal/config/config.go`) —
   a test fails for any `cmd/` directory without a unique one — and pass the
   same name to `config.LoadConfig("<name>")`. Add the name to `SERVICES` in
   the `Makefile` and to
   `scripts/build-arm64.sh`, plus the matrix in
   `.github/workflows/build.yml`.
3. Decide the security posture: does it need a mandatory key like
   `mcp-termux`/`mcp-filesystem`? (Enforce it for the HTTP listener only —
   `if apiKey == "" && !cfg.Stdio()` — since stdio has nothing to guard.) Which `DROIDMCP_<NAME>_*` variables gate the
   risky parts? Document them in `docs/security.md`.
4. Document the server in both READMEs and the ROADMAP.

## Pull requests

1. Fork and create a feature branch from `main`.
2. Keep PRs focused — one feature or fix per PR.
3. Make sure `make test`, `make lint`, and `make sec` pass locally (or at
   least `make test` + `go vet` if you can't install the linters; CI will
   catch the rest).
4. Use short, imperative commit summaries
   (e.g. `Add get_storage tool to mcp-termux`).
5. Describe **what** and **why** in the PR body; link the ROADMAP phase or
   issue it addresses.

Looking for something to work on? Open phases and pending tasks live in
[ROADMAP.md](ROADMAP.md).

## Reporting security issues

Please do **not** open a public issue for vulnerabilities. The reporting
process and what is in scope are in [SECURITY.md](SECURITY.md).

## License

By contributing you agree that your contributions are licensed under the
[MIT License](LICENSE).
