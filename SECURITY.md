# Security Policy

DroidMCP servers can read files, run commands, send SMS and act on GitHub on
the device owner's behalf, so security reports are very welcome.

## Supported versions

Fixes land on `main` and ship in the next tagged release. Only the latest
release is supported; check which one you run with `curl -fsS
http://localhost:<port>/healthz`.

## Reporting a vulnerability

Please do **not** open a public issue. Use GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on this repository (**Security → Report a vulnerability**).

A useful report names the server and version, the configuration involved
(transport, which `DROIDMCP_*` variables are set — never their secret values),
the steps or tool calls that reproduce it, and what an attacker gains.

## Scope

The threat model is in [`docs/security.md`](docs/security.md#threat-model).

- **In scope:** anything that breaks a documented guarantee — escaping
  `DROIDMCP_ROOT`, bypassing the API key, the `Host`/`Origin` guard, the
  scraper's SSRF checks, the network server's private-target rule, the
  `mcp-termux` or `mcp-automation` allowlists, the SQL file-access guards,
  `read_env` or log redaction, or a disabled tool still being callable.
- **Out of scope:** deployments that switch a protection off on purpose (no key
  on a non-loopback interface, `DROIDMCP_TERMUX_ALLOWLIST=*`,
  `DROIDMCP_SCRAPER_ALLOW_PRIVATE=1`, …), and attackers who already run as the
  Termux user or root, which the threat model excludes.

## Verifying releases

Every release ships a cosign-signed `SHA256SUMS`. How to check a download
against it is described in
[`docs/security.md`](docs/security.md#verifying-a-release).
