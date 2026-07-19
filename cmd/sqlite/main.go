// Command sqlite provides an MCP server for lightweight, local SQLite database
// operations. Databases live as files under DROIDMCP_ROOT; every path argument
// is validated against that root to prevent directory traversal, and all
// user-supplied values are bound as parameters so statements are injection-safe.
// Because the server both creates files under ROOT and executes arbitrary SQL,
// it refuses to start without DROIDMCP_ROOT and an API key — mirroring
// mcp-filesystem and mcp-media.
//
// The database engine is modernc.org/sqlite, a pure-Go (CGO-free) SQLite
// implementation, so the binary keeps DroidMCP's zero-dependency, single-file
// ARM64 build.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// Registers the "sqlite" database/sql driver (pure Go, no CGO).
	_ "modernc.org/sqlite"

	"github.com/kahz12/droidmcp/internal/buildinfo"
	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
)

var cfg *config.Config

func main() {
	var err error
	cfg, err = config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	// Require an explicit DROIDMCP_ROOT. The shared config defaults ROOT to "/",
	// which would let the server open (and create) database files anywhere on the
	// device; like mcp-filesystem and mcp-media, this server fail-fasts rather
	// than silently acting on the whole filesystem.
	if os.Getenv("DROIDMCP_ROOT") == "" {
		logger.Log.Error("mcp-sqlite requires DROIDMCP_ROOT to be set to the directory it may access. Refusing to start (the default of \"/\" would expose the whole device).")
		os.Exit(1)
	}

	// This server creates files under ROOT and executes arbitrary SQL, so it must
	// not run unauthenticated: anything else on localhost (other apps, adb) could
	// otherwise drive it. Require an API key, mirroring mcp-filesystem/mcp-media.
	apiKey := config.ResolveAPIKey("sqlite")
	if apiKey == "" {
		logger.Log.Error("mcp-sqlite requires DROIDMCP_SQLITE_KEY or DROIDMCP_API_KEY to be set. Refusing to start.")
		os.Exit(1)
	}

	server := core.NewDroidServer("mcp-sqlite", buildinfo.Version)
	server.APIKey = apiKey
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

// registerTools maps MCP tool definitions to their Go handlers.
func registerTools(s *core.DroidServer) {
	// open_db: open (or create) a database file and report basic facts.
	s.MCPServer.AddTool(mcp.NewTool("open_db",
		mcp.WithDescription("Open a SQLite database, creating the file if it does not exist. Returns {path, created, sqlite_version}. Other tools accept the same `db` path and open it lazily, so calling this first is optional."),
		mcp.WithString("db", mcp.Required(), mcp.Description("Database file path relative to root (e.g. \"data/app.db\")")),
	), handleOpenDB)

	// query: run a read statement and return its rows.
	s.MCPServer.AddTool(mcp.NewTool("query",
		mcp.WithDescription("Run a read-only statement (SELECT/WITH/PRAGMA/EXPLAIN/VALUES) and return the rows as JSON. Use `args` for parameter placeholders (?) to stay injection-safe."),
		mcp.WithString("db", mcp.Required(), mcp.Description("Database file path relative to root")),
		mcp.WithString("sql", mcp.Required(), mcp.Description("The SQL statement to execute; use ? placeholders for values")),
		mcp.WithArray("args", mcp.Items(map[string]any{}),
			mcp.Description("Positional parameters bound to the ? placeholders, in order. Accepts strings, numbers, booleans and null")),
		mcp.WithNumber("max_rows", mcp.Description("Cap the number of returned rows. Default 1000; 0 means unlimited")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 30s, max 600s")),
	), handleQuery)

	// execute: run a write statement and report its effect.
	s.MCPServer.AddTool(mcp.NewTool("execute",
		mcp.WithDescription("Run a write statement (INSERT/UPDATE/DELETE/CREATE/DROP/…) and return {rows_affected, last_insert_id}. Use `args` for parameter placeholders (?) to stay injection-safe."),
		mcp.WithString("db", mcp.Required(), mcp.Description("Database file path relative to root")),
		mcp.WithString("sql", mcp.Required(), mcp.Description("The SQL statement to execute; use ? placeholders for values")),
		mcp.WithArray("args", mcp.Items(map[string]any{}),
			mcp.Description("Positional parameters bound to the ? placeholders, in order")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 30s, max 600s")),
	), handleExecute)

	// list_tables: enumerate user tables and views.
	s.MCPServer.AddTool(mcp.NewTool("list_tables",
		mcp.WithDescription("List the user tables and views in a database (internal sqlite_* objects are excluded). Returns a JSON array of {name, type}."),
		mcp.WithString("db", mcp.Required(), mcp.Description("Database file path relative to root")),
	), handleListTables)

	// describe_table: column schema for a single table.
	s.MCPServer.AddTool(mcp.NewTool("describe_table",
		mcp.WithDescription("Describe a table's columns (via PRAGMA table_info). Returns {table, columns:[{cid, name, type, notnull, default, pk}]}."),
		mcp.WithString("db", mcp.Required(), mcp.Description("Database file path relative to root")),
		mcp.WithString("table", mcp.Required(), mcp.Description("Name of the table or view to describe")),
	), handleDescribeTable)

	// export_csv: stream a query's results into a CSV file under root.
	s.MCPServer.AddTool(mcp.NewTool("export_csv",
		mcp.WithDescription("Run a SELECT and stream the results into a CSV file under root (header row + one row per record). Returns {path, rows, columns}."),
		mcp.WithString("db", mcp.Required(), mcp.Description("Database file path relative to root")),
		mcp.WithString("sql", mcp.Required(), mcp.Description("The SELECT statement whose rows are exported; use ? placeholders for values")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Destination CSV path relative to root; parent directories are created")),
		mcp.WithArray("args", mcp.Items(map[string]any{}),
			mcp.Description("Positional parameters bound to the ? placeholders, in order")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 30s, max 600s")),
	), handleExportCSV)
}

// securePath resolves a relative path against DROIDMCP_ROOT and ensures it stays
// within bounds. It returns an absolute path or an error if a traversal attempt
// is detected. It is identical in behaviour to mcp-filesystem/mcp-media's
// securePath: a lexical containment check plus a symlink-escape check that fails
// closed.
func securePath(relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", relPath)
	}
	absRoot, err := filepath.Abs(cfg.Root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absRoot, relPath)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if !withinRoot(absRoot, absTarget) {
		return "", errors.New("access denied: path escapes root")
	}
	if err := checkNoSymlinkEscape(absRoot, absTarget); err != nil {
		return "", err
	}
	return absTarget, nil
}

// withinRoot reports whether absTarget is root itself or a descendant of it.
// Using root+separator prevents prefix false positives (/tmp/safe vs
// /tmp/safevil).
func withinRoot(root, absTarget string) bool {
	return absTarget == root || strings.HasPrefix(absTarget, root+string(filepath.Separator))
}

// checkNoSymlinkEscape resolves symlinks in absTarget (and every parent
// component) and verifies the real path stays within the real root. absTarget
// need not exist yet: the longest existing ancestor is resolved and checked.
// Any resolution error other than "does not exist" fails closed.
func checkNoSymlinkEscape(absRoot, absTarget string) error {
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("cannot resolve root: %w", err)
	}
	cur := absTarget
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if !withinRoot(realRoot, resolved) {
				return errors.New("access denied: path escapes root via symlink")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("access denied: %w", err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return errors.New("access denied: path escapes root")
		}
		cur = parent
	}
}
