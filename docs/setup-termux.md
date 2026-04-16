# Termux Setup Guide for DroidMCP

This guide will help you set up and run DroidMCP servers on your Android device using Termux.

## Prerequisites

1. Install **Termux** (preferably from F-Droid).
2. Open Termux and update the package list:
   ```bash
   pkg update && pkg upgrade
   ```

## Install Dependencies

DroidMCP requires Go and Make to build the binaries.

```bash
pkg install golang git make
```

## Building the Servers

Clone the repository and run the build command:

```bash
git clone https://github.com/kahz12/DroidMCP
cd DroidMCP
make build
```

## Configuring Environment Variables

Each server can be configured via environment variables.

| Variable | Description | Default |
|----------|-------------|---------|
| `DROIDMCP_PORT` | Port the server will listen on | `3000` |
| `DROIDMCP_ROOT` | (Filesystem) Root directory for file operations | `/` |
| `GITHUB_TOKEN` | (GitHub) Your Personal Access Token | Required for GitHub MCP |

## Running the Servers

To run a server, navigate to the `bin` directory and execute the binary. It's recommended to run them in the background or using a terminal multiplexer like `tmux`.

```bash
# Example: Run filesystem MCP on port 8080
export DROIDMCP_PORT=8080
export DROIDMCP_ROOT=/sdcard/Documents
./bin/droidmcp-filesystem
```

## Connecting to Clients

### Claude Desktop / Claude Code
Use the SSE endpoint: `http://localhost:8080/sse`.

### Gemini CLI
Add the server configuration to your `gemini.yaml` or connect via the SSE URL.

## Security Note

DroidMCP servers are designed to run on `localhost`. **Never expose these ports to external networks**, as they provide significant control over your device (especially the `termux` and `filesystem` MCPs).
