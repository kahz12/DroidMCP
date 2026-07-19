# DroidMCP Roadmap

> Native MCP servers for Android/Termux — ARM64 binaries with zero external dependencies.
> **Stack:** Go · HTTP/SSE · Monorepo · Target: Linux ARM64

---

## Overview

DroidMCP is a collection of MCP (Model Context Protocol) servers designed to run
natively on Android through Termux. No Node.js, no Python, no dependencies —
just a binary that works.

```
Claude Code / Gemini CLI
        |
        | HTTP/SSE (MCP Protocol)
        v
  DroidMCP Server  <-- runs on Termux (Android)
        |
   +----+--------------------+
   v    v                    v
 Files  GitHub            Scraper ...
```

---

## Tech Stack

| Component        | Technology                  |
|------------------|-----------------------------|
| Language         | Go                          |
| MCP Transport    | HTTP/SSE                    |
| MCP SDK          | `mark3labs/mcp-go`          |
| GitHub API       | `google/go-github`          |
| Scraping         | `gocolly/colly`             |
| Config           | `spf13/viper`               |
| CLI              | `spf13/cobra`               |
| Build target     | `GOOS=linux GOARCH=arm64`   |
| Structure        | Monorepo                    |

---

## Repository Structure

```
DroidMCP/
├── cmd/
│   ├── filesystem/
│   │   └── main.go
│   ├── github/
│   │   └── main.go
│   ├── scraper/
│   │   └── main.go
│   ├── termux/
│   │   └── main.go
│   ├── clipboard/
│   │   └── main.go
│   └── network/
│       └── main.go
├── internal/
│   ├── core/
│   │   └── server.go
│   ├── logger/
│   │   └── logger.go
│   ├── config/
│   │   └── config.go
│   └── buildinfo/
│       └── buildinfo.go
├── scripts/
│   └── build-arm64.sh
├── docs/
│   ├── setup-termux.md
│   └── security.md
├── .github/
│   └── workflows/
│       └── build.yml
├── Makefile
├── go.mod
├── ROADMAP.md
└── README.md
```

---

## PHASE 0 — Foundation
> **Goal:** Functional repo with shared core and ARM64 build pipeline

### Initial Setup
- [x] Create `DroidMCP` repo on GitHub
- [x] Install Go on Termux (`pkg install golang`)
- [x] Initialize monorepo with `go mod init github.com/kahz12/droidmcp`
- [x] Define code conventions and folder structure

### Shared Core `internal/`
- [x] `internal/core/server.go` — Reusable MCP base server with HTTP/SSE
- [x] `internal/logger/logger.go` — Shared structured logger
- [x] `internal/config/config.go` — Environment variable config loader

### Build Pipeline
- [x] `scripts/build-arm64.sh` — Compiles all binaries for ARM64
- [x] `Makefile` — Commands: `build`, `test`, `clean`, `install`
- [x] `.github/workflows/build.yml` — CI/CD: auto build + release on each tag

---

## PHASE 1 — mcp-filesystem
> **Goal:** First functional MCP — expose Android directories to Claude Code / Gemini CLI

### MCP Tools
| Tool              | Description                          |
|-------------------|--------------------------------------|
| `read_file`       | Read file contents                   |
| `write_file`      | Write/create a file                  |
| `list_directory`  | List directory contents              |
| `search_files`    | Search files by name or pattern      |
| `delete_file`     | Delete a file                        |
| `move_file`       | Move or rename a file                |

### Tasks
- [x] Implement each tool with robust error handling
- [x] Respect Android permissions (scoped storage)
- [x] Configure root directory via `DROIDMCP_ROOT` env var
- [x] Unit tests for each tool
- [x] Documentation: `docs/setup-termux.md`
- [x] Integration guide for Claude Code and Gemini CLI

---

## PHASE 2 — mcp-github
> **Goal:** Full GitHub operations from Android without Node or npm

### MCP Tools
| Tool              | Description                          |
|-------------------|--------------------------------------|
| `list_repos`      | List user repositories               |
| `get_repo`        | Detailed repo info                   |
| `create_issue`    | Open an issue                        |
| `list_issues`     | List issues from a repo              |
| `get_pr`          | Get pull request details             |
| `create_pr`       | Create a Pull Request                |
| `commit_file`     | Commit a file                        |
| `get_file`        | Read a file from the repo            |

### Tasks
- [x] Auth via `GITHUB_TOKEN` (Personal Access Token)
- [x] Integrate `google/go-github`
- [x] Rate limiting handler
- [x] Tests with GitHub API mock
- [x] Documentation and examples

---

## PHASE 3 — mcp-scraper
> **Goal:** Lightweight scraping without Chromium or Playwright — native ARM64

### MCP Tools
| Tool               | Description                              |
|--------------------|------------------------------------------|
| `fetch_page`       | Fetch HTML from a URL                    |
| `extract_text`     | Extract clean text from a page           |
| `extract_links`    | Extract all links from a page            |
| `search_in_page`   | Search for text or pattern in a page     |
| `extract_table`    | Extract HTML tables as JSON              |
| `extract_metadata` | Extract title, description, og:*, twitter:* |

### Tasks
- [x] Integrate `gocolly/colly` + `goquery`
- [x] Configurable user-agent
- [x] Rate limiting and timeout handling
- [x] Basic custom headers support
- [x] Documentation with real-world use cases

---

## PHASE 4 — mcp-termux
> **Goal:** Give Claude hands inside Termux itself

### MCP Tools
| Tool              | Description                          |
|-------------------|--------------------------------------|
| `run_command`     | Execute a command in Termux          |
| `install_pkg`     | Install a package with pkg           |
| `list_pkgs`       | List installed packages              |
| `read_env`        | Read environment variables           |
| `get_storage`     | Get available storage info           |
| `termux_*`        | Termux:API wrappers: battery, location, notification, toast, sms_send, tts_speak |

### Tasks
- [x] Security sandbox — whitelist of allowed commands
- [x] Configurable timeout per command
- [x] Log all executed commands
- [x] Documentation on risks and secure configuration

---

## PHASE 5 — mcp-network (DroidNet Integration) [DONE]
> **Goal:** Integrate DroidNet Sentinel capabilities as an MCP
> Implemented in pure Go, no Scapy/DroidNet subprocess needed

### MCP Tools
| Tool               | Description                              |
|--------------------|------------------------------------------|
| `scan_network`     | Scan devices on local network            |
| `check_ports`      | Port scan a device                       |
| `nslookup`         | Forward DNS lookup                       |
| `reverse_dns`      | Reverse DNS lookup                       |
| `traceroute`       | Trace path to a host                     |
| `network_info`     | Gateway, DNS servers, interfaces, subnet |
| `get_device_info`  | Detailed info about a known device       |
| `list_devices`     | List all known devices                   |

### Tasks
- [x] Pure Go implementation (no Scapy/DroidNet subprocess needed)
- [x] `scan_network` + `check_ports` with private-target guard (`DROIDMCP_NETWORK_ALLOW_PUBLIC` opt-in)
- [x] DNS, traceroute, and network info tools with unit tests
- [x] Persistent device inventory (`scan_network` feeds it; `get_device_info` / `list_devices` read it), path via `DROIDMCP_NETWORK_DB`
- [x] Documentation on requirements and scoping (`docs/security.md`)

---

## PHASE 6 — Polish & Community
> **Goal:** Project ready for open source community

- [x] Complete README in English (includes Claude Code / Gemini CLI integration)
- [x] Spanish translation of the README (`README.es.md`)
- [x] Core documentation in `docs/` (`setup-termux.md`, `security.md`)
- [x] Dedicated usage guides in `docs/` (`usage.md` + `usage.es.md`): full tool-by-tool reference, config, recipes, troubleshooting
- [ ] Demo video running on real Android device
- [x] Publish to `awesome-mcp-servers`
- [x] Publish to `awesome-termux`
- [x] First official release with all ARM64 binaries (`v0.1.0`)
- [x] Contributing guide for new collaborators (`CONTRIBUTING.md`)

---

## PHASE 7 — mcp-clipboard [DONE]
> **Goal:** Clipboard management between Android and AI agents

### MCP Tools
| Tool                | Description                                  |
|---------------------|----------------------------------------------|
| `get_clipboard`     | Read current clipboard content               |
| `set_clipboard`     | Write text to clipboard                      |
| `clipboard_history` | Retrieve clipboard history (if available)    |

### Tasks
- [x] Integrate `termux-clipboard-get` and `termux-clipboard-set`
- [x] Handle text input/output via standard streams
- [x] Implementation of `clipboard_history` (stub as not supported by API)
- [x] Integration into build pipeline (Makefile/scripts)

---

## Future MCP Ideas

### PHASE 8 — mcp-notifications
> **Goal:** Send and read Android notifications from AI agents

#### MCP Tools
| Tool                  | Description                                |
|-----------------------|--------------------------------------------|
| `send_notification`   | Push a notification to the Android device  |
| `list_notifications`  | List active notifications                  |
| `dismiss_notification`| Dismiss a specific notification            |
| `get_dnd_status`      | Check Do Not Disturb status                |

#### Tasks
- [ ] Integrate `termux-notification` for sending notifications with title, content, and ID
- [ ] Implement `list_notifications` via `termux-notification-list`
- [ ] Implement `dismiss_notification` via `termux-notification-remove`
- [ ] Handle `get_dnd_status` via `termux-volume` or stub if unsupported by API
- [ ] Integration into build pipeline (Makefile/scripts)

---

### PHASE 9 — mcp-contacts
> **Goal:** Read-only access to Android contacts for AI-assisted workflows

#### MCP Tools
| Tool                | Description                                  |
|---------------------|----------------------------------------------|
| `search_contacts`   | Search contacts by name, phone, or email     |
| `get_contact`       | Get full details of a contact                |
| `list_groups`       | List contact groups                          |
| `export_contacts`   | Export contacts as vCard/JSON                |

#### Tasks
- [ ] Integrate `termux-contact-list` for full contact retrieval
- [ ] Implement `search_contacts` with in-memory filtering by name, phone, and email
- [ ] Implement `get_contact` returning structured JSON with all available fields
- [ ] Implement `list_groups` (stub if not supported by Termux API)
- [ ] Implement `export_contacts` serializing results to vCard or JSON format
- [ ] Integration into build pipeline (Makefile/scripts)

---

### PHASE 10 — mcp-calendar
> **Goal:** Calendar integration for scheduling and event management

#### MCP Tools
| Tool                | Description                                  |
|---------------------|----------------------------------------------|
| `list_events`       | List upcoming events                         |
| `create_event`      | Create a new calendar event                  |
| `update_event`      | Modify an existing event                     |
| `delete_event`      | Remove a calendar event                      |
| `check_availability`| Check free/busy time slots                   |

#### Tasks
- [ ] Integrate `termux-calendar-list` for reading events with date range filter
- [ ] Implement `create_event` with title, start/end time, and optional description
- [ ] Implement `update_event` by event ID using Termux calendar API
- [ ] Implement `delete_event` with confirmation of successful removal
- [ ] Implement `check_availability` by scanning event slots for free/busy windows
- [ ] Integration into build pipeline (Makefile/scripts)

---

### PHASE 11 — mcp-media [DONE]
> **Goal:** Manage photos, videos, and audio files on the device

#### MCP Tools
| Tool                | Description                                  |
|---------------------|----------------------------------------------|
| `list_media`        | List media files (photos, videos, audio)     |
| `get_metadata`      | Read EXIF/metadata from a media file         |
| `convert_image`     | Convert image format or resize               |
| `extract_audio`     | Extract audio from video files               |
| `thumbnail`         | Generate thumbnail for a media file          |

#### Tasks
- [x] Implement `list_media` by scanning configurable directories for image, video, and audio extensions (recursion + kind filter + `max_results`)
- [x] Implement `get_metadata` in pure Go (stdlib image dimensions) enriched with `exiftool -json` tags when available
- [x] Implement `convert_image` and `thumbnail` via `ffmpeg` subprocess (scale filter, JPEG quality mapping, video frame seek)
- [x] Implement `extract_audio` from video using `ffmpeg -vn` (stream copy by default, optional re-encode)
- [x] Validate all input paths against `DROIDMCP_ROOT` to prevent path traversal (lexical + symlink-escape checks); server refuses to start without an API key
- [x] Integration into build pipeline (Makefile / `scripts/build-arm64.sh` / release workflow)
- [x] Documentation: server tables in both READMEs and a full `### mcp-media` section in `docs/usage.md` + `docs/usage.es.md`

---

### PHASE 12 — mcp-sms
> **Goal:** SMS management via Termux:API for AI-powered messaging workflows

#### MCP Tools
| Tool                | Description                                  |
|---------------------|----------------------------------------------|
| `list_sms`          | List received/sent SMS messages              |
| `send_sms`          | Send an SMS message                          |
| `search_sms`        | Search messages by content or contact        |

#### Tasks
- [ ] Integrate `termux-sms-list` with inbox/sent/outbox type filter and count limit
- [ ] Implement `send_sms` via `termux-sms-send` with number and body validation
- [ ] Implement `search_sms` with in-memory filtering by content or contact number
- [ ] Handle Termux:API permission errors with descriptive error messages
- [ ] Integration into build pipeline (Makefile/scripts)

---

### PHASE 13 — mcp-sensors
> **Goal:** Access Android hardware sensors for IoT and automation use cases

#### MCP Tools
| Tool                | Description                                  |
|---------------------|----------------------------------------------|
| `get_battery`       | Battery level, charging status, health       |
| `get_location`      | Current GPS coordinates                      |
| `get_wifi_info`     | Current WiFi network info                    |
| `get_brightness`    | Screen brightness level                      |
| `get_volume`        | Current volume levels                        |
| `list_sensors`      | List all available hardware sensors          |

#### Tasks
- [ ] Implement `get_battery` via `termux-battery-status` returning level, status, and health
- [ ] Implement `get_location` via `termux-location` with configurable provider (gps/network/passive)
- [ ] Implement `get_wifi_info` via `termux-wifi-connectioninfo` returning SSID, signal, and IP
- [ ] Implement `get_brightness` and `get_volume` via `termux-brightness` and `termux-volume`
- [ ] Implement `list_sensors` aggregating availability of each sensor tool
- [ ] Integration into build pipeline (Makefile/scripts)

---

### PHASE 14 — mcp-sqlite [DONE]
> **Goal:** Lightweight database operations for local data management

#### MCP Tools
| Tool                | Description                                  |
|---------------------|----------------------------------------------|
| `open_db`           | Open or create a SQLite database             |
| `query`             | Execute a SELECT query                       |
| `execute`           | Execute INSERT/UPDATE/DELETE statements       |
| `list_tables`       | List all tables in a database                |
| `describe_table`    | Get schema of a table                        |
| `export_csv`        | Export query results as CSV                  |

#### Tasks
- [x] Add `modernc.org/sqlite` as dependency (pure Go, no CGO required on ARM64)
- [x] Implement `open_db` with path validation against `DROIDMCP_ROOT` (creates the file + parent dirs; other tools require it to exist)
- [x] Implement `query` and `execute` using parameterized statements to prevent SQL injection (`query` is read-only-guarded; values bound via `args`)
- [x] Implement `list_tables` and `describe_table` via `sqlite_master` schema queries (table name validated before `PRAGMA table_info`)
- [x] Implement `export_csv` streaming query results row-by-row into CSV format
- [x] Integration into build pipeline (Makefile / `scripts/build-arm64.sh` / release workflow)
- [x] Documentation: server tables in both READMEs and a full `### mcp-sqlite` section in `docs/usage.md` + `docs/usage.es.md`

---

### PHASE 15 — mcp-llm-proxy
> **Goal:** Proxy local LLMs (llama.cpp, Ollama) running on device as MCP tools

#### MCP Tools
| Tool                | Description                                  |
|---------------------|----------------------------------------------|
| `list_models`       | List available local models                  |
| `generate`          | Generate text with a local model             |
| `embed`             | Generate embeddings from text                |
| `model_info`        | Get model metadata and capabilities          |

#### Tasks
- [ ] Implement `list_models` by querying Ollama REST API (`GET /api/tags`)
- [ ] Implement `generate` via Ollama `/api/generate` with streaming disabled for simplicity
- [ ] Implement `embed` via Ollama `/api/embeddings` returning float slice as JSON
- [ ] Implement `model_info` via Ollama `/api/show` for metadata and parameter count
- [ ] Support configurable Ollama host via `DROIDMCP_OLLAMA_HOST` env var (default `localhost:11434`)
- [ ] Integration into build pipeline (Makefile/scripts)

---

### PHASE 16 — mcp-automation
> **Goal:** Task automation and cron-like scheduling on Android

#### MCP Tools
| Tool                | Description                                  |
|---------------------|----------------------------------------------|
| `create_task`       | Schedule a recurring task                    |
| `list_tasks`        | List all scheduled tasks                     |
| `run_task`          | Manually trigger a scheduled task            |
| `delete_task`       | Remove a scheduled task                      |
| `task_history`      | View execution history of a task             |

#### Tasks
- [ ] Design task persistence schema using a local JSON file or SQLite store
- [ ] Implement `create_task` accepting a cron expression or interval with a shell command payload
- [ ] Implement `list_tasks` and `task_history` reading from the persistent store
- [ ] Implement `run_task` for manual trigger with stdout/stderr output capture
- [ ] Implement `delete_task` with cleanup of any associated scheduler entries
- [ ] Integration into build pipeline (Makefile/scripts)

---

*DroidMCP — Made from Android, for Android.*
