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
`make build-arm64` (cross-compile via `scripts/build-arm64.sh`).

CI runs **test + lint + gosec** on every push and PR; all three must be green.

## Repository layout

```
cmd/<server>/     one MCP server per directory (main.go registers tools)
internal/core     shared HTTP/SSE server wrapper (auth, TLS, /healthz)
internal/config   DROIDMCP_* environment configuration
internal/logger   structured logging with secret redaction
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
4. Update the tool tables in `README.md`, `README.es.md`, and `ROADMAP.md`.

## Adding a new server

1. Create `cmd/<name>/main.go` following an existing server:
   `core.NewDroidServer("mcp-<name>", buildinfo.Version)`, resolve the API key
   with `config.ResolveAPIKey("<name>")`, register tools, `ServeSSE(cfg.Port)`.
2. Add the name to `SERVICES` in the `Makefile` and to
   `scripts/build-arm64.sh`, plus the matrix in
   `.github/workflows/build.yml`.
3. Decide the security posture: does it need a mandatory key like
   `mcp-termux`/`mcp-filesystem`? Which `DROIDMCP_<NAME>_*` variables gate the
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

Please do **not** open a public issue for vulnerabilities. Use GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on this repository. Read `docs/security.md` for the threat model — reports
about misconfigured deployments (e.g. no API key on a public interface) are
out of scope, but anything that breaks a documented guarantee is very much in
scope.

## License

By contributing you agree that your contributions are licensed under the
[MIT License](LICENSE).
