package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// Per-call timeout bounds, shared by query/execute/export_csv.
const (
	defaultTimeout = 30 * time.Second
	maxTimeout     = 600 * time.Second
	defaultMaxRows = 1000
)

// dbCache memoizes one *sql.DB per resolved database path so repeated tool calls
// reuse a single connection pool instead of reopening the file each time. The
// process is short-lived per request stream, so handles are intentionally never
// evicted; they are released when the process exits.
var (
	dbMu    sync.Mutex
	dbCache = map[string]*sql.DB{}
)

// getDB returns a ready connection pool for the database at absPath, opening it
// on first use. The pool is capped at a single open connection: SQLite
// serializes writers per file, and one connection sidesteps "database is locked"
// races between concurrent tool calls in this process. A busy_timeout is set so
// the rare cross-process contention waits briefly instead of failing outright.
func getDB(absPath string) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db, ok := dbCache[absPath]; ok {
		return db, nil
	}
	db, err := sql.Open("sqlite", absPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// Ping forces the driver to actually open (and, for open_db, create) the
	// file so a bad path fails here with a clear error.
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return nil, err
	}
	dbCache[absPath] = db
	return db, nil
}

// dbForQuery resolves a root-relative database path that must already exist and
// returns its connection pool. Only open_db is allowed to create databases; the
// read/write tools refuse a missing file so a typo does not silently leave an
// empty database behind. On failure it returns an error tool result ready to be
// returned to the caller.
func dbForQuery(rel string) (*sql.DB, *mcp.CallToolResult) {
	abs, err := securePath(rel)
	if err != nil {
		return nil, mcp.NewToolResultError(err.Error())
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, mcp.NewToolResultError(fmt.Sprintf("database %q does not exist; call open_db first", rel))
		}
		return nil, mcp.NewToolResultError(err.Error())
	}
	if info.IsDir() {
		return nil, mcp.NewToolResultError(fmt.Sprintf("%q is a directory, not a database file", rel))
	}
	db, err := getDB(abs)
	if err != nil {
		return nil, mcp.NewToolResultError(err.Error())
	}
	return db, nil
}

// callContext derives a context bounded by the request's timeout_seconds
// parameter (clamped to (0, maxTimeout]).
func callContext(parent context.Context, req mcp.CallToolRequest) (context.Context, context.CancelFunc) {
	secs := req.GetInt("timeout_seconds", int(defaultTimeout.Seconds()))
	d := time.Duration(secs) * time.Second
	if d <= 0 {
		d = defaultTimeout
	}
	if d > maxTimeout {
		d = maxTimeout
	}
	return context.WithTimeout(parent, d)
}

// toolArgs reads the optional "args" array and normalizes each element for
// binding. Numbers arrive from JSON as float64; integral values are converted to
// int64 so they bind as SQLite integers rather than reals.
func toolArgs(req mcp.CallToolRequest) ([]any, error) {
	raw, ok := req.GetArguments()["args"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.New("\"args\" must be an array of positional parameters")
	}
	out := make([]any, len(list))
	for i, v := range list {
		out[i] = normalizeArg(v)
	}
	return out, nil
}

func normalizeArg(v any) any {
	if f, ok := v.(float64); ok && !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f) {
		return int64(f)
	}
	return v
}

// readKeywords is the set of statement kinds the `query` tool accepts. Write
// statements are rejected there so an agent cannot mutate data through a tool
// documented as read-only; they belong in `execute`.
var readKeywords = map[string]bool{
	"SELECT": true, "WITH": true, "PRAGMA": true, "EXPLAIN": true, "VALUES": true,
}

// leadingKeyword returns the uppercased first token of a SQL statement, or "" if
// there is none. A statement wrapped in parentheses (e.g. "(SELECT …)") is
// treated as a SELECT.
func leadingKeyword(sqlText string) string {
	s := strings.TrimSpace(sqlText)
	if s == "" {
		return ""
	}
	if s[0] == '(' {
		return "SELECT"
	}
	i := 0
	for i < len(s) && !isSpaceByte(s[i]) && s[i] != '(' {
		i++
	}
	return strings.ToUpper(s[:i])
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// normalizeValue converts a scanned SQL value into a JSON-friendly form. SQLite
// TEXT/BLOB columns scan as []byte; they are returned as strings (BLOBs are
// therefore lossy for non-UTF-8 data, which is an accepted trade-off for this
// tool's text-oriented output).
func normalizeValue(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// scanRows reads up to maxRows rows into column-keyed maps. maxRows == 0 means
// unlimited. It reports whether more rows were available than were returned.
func scanRows(rows *sql.Rows, maxRows int) (cols []string, out []map[string]any, truncated bool, err error) {
	cols, err = rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	out = make([]map[string]any, 0, 16)
	for rows.Next() {
		if maxRows > 0 && len(out) >= maxRows {
			truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return nil, nil, false, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalizeValue(vals[i])
		}
		out = append(out, row)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, false, err
	}
	return cols, out, truncated, nil
}

func handleOpenDB(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, err := req.RequireString("db")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	abs, err := securePath(rel)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Record existence before opening so we can report whether this call created
	// the file.
	created := false
	if info, statErr := os.Stat(abs); os.IsNotExist(statErr) {
		created = true
	} else if statErr != nil {
		return mcp.NewToolResultError(statErr.Error()), nil
	} else if info.IsDir() {
		return mcp.NewToolResultError(fmt.Sprintf("%q is a directory, not a database file", rel)), nil
	}

	// SQLite cannot create the file in a missing directory; make the parent path
	// (which securePath already confined to root) before opening.
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	db, err := getDB(abs)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var version string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return jsonResult(map[string]any{
		"path":           rel,
		"created":        created,
		"sqlite_version": version,
	})
}

func handleQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, err := req.RequireString("db")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sqlText, err := req.RequireString("sql")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if kw := leadingKeyword(sqlText); !readKeywords[kw] {
		return mcp.NewToolResultError(fmt.Sprintf("query only runs read statements (SELECT/WITH/PRAGMA/EXPLAIN/VALUES); got %q — use execute for writes", kw)), nil
	}
	args, err := toolArgs(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	maxRows := req.GetInt("max_rows", defaultMaxRows)
	if maxRows < 0 {
		return mcp.NewToolResultError("max_rows must be >= 0"), nil
	}

	db, errRes := dbForQuery(rel)
	if errRes != nil {
		return errRes, nil
	}

	cctx, cancel := callContext(ctx, req)
	defer cancel()

	rows, err := db.QueryContext(cctx, sqlText, args...)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer rows.Close()

	cols, data, truncated, err := scanRows(rows, maxRows)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return jsonResult(map[string]any{
		"columns":   cols,
		"rows":      data,
		"count":     len(data),
		"truncated": truncated,
	})
}

func handleExecute(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, err := req.RequireString("db")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sqlText, err := req.RequireString("sql")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	args, err := toolArgs(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	db, errRes := dbForQuery(rel)
	if errRes != nil {
		return errRes, nil
	}

	cctx, cancel := callContext(ctx, req)
	defer cancel()

	res, err := db.ExecContext(cctx, sqlText, args...)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	out := map[string]any{}
	if affected, aerr := res.RowsAffected(); aerr == nil {
		out["rows_affected"] = affected
	}
	if lastID, ierr := res.LastInsertId(); ierr == nil {
		out["last_insert_id"] = lastID
	}
	return jsonResult(out)
}

// tableRow is one entry returned by list_tables.
type tableRow struct {
	Name string `json:"name"`
	Type string `json:"type"` // "table" or "view"
}

func handleListTables(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, err := req.RequireString("db")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	db, errRes := dbForQuery(rel)
	if errRes != nil {
		return errRes, nil
	}

	cctx, cancel := callContext(ctx, req)
	defer cancel()

	rows, err := db.QueryContext(cctx,
		`SELECT name, type FROM sqlite_master
		 WHERE type IN ('table','view') AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
		 ORDER BY name`)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer rows.Close()

	out := make([]tableRow, 0, 16)
	for rows.Next() {
		var tr tableRow
		if err := rows.Scan(&tr.Name, &tr.Type); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out = append(out, tr)
	}
	if err := rows.Err(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(out)
}

// columnInfo is one column entry returned by describe_table (PRAGMA table_info).
type columnInfo struct {
	CID     int    `json:"cid"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	NotNull bool   `json:"notnull"`
	Default any    `json:"default"`
	PK      int    `json:"pk"`
}

func handleDescribeTable(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, err := req.RequireString("db")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	table, err := req.RequireString("table")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	db, errRes := dbForQuery(rel)
	if errRes != nil {
		return errRes, nil
	}

	cctx, cancel := callContext(ctx, req)
	defer cancel()

	// PRAGMA does not accept bound parameters, so confirm the table exists with a
	// parameterized lookup first, then interpolate a properly quoted identifier.
	var exists string
	err = db.QueryRowContext(cctx,
		`SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name = ? LIMIT 1`,
		table).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return mcp.NewToolResultError(fmt.Sprintf("no such table or view: %q", table)), nil
	}
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	rows, err := db.QueryContext(cctx, "PRAGMA table_info("+quoteIdent(exists)+")")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer rows.Close()

	cols := make([]columnInfo, 0, 8)
	for rows.Next() {
		var (
			ci      columnInfo
			notnull int
			dflt    sql.NullString
		)
		if err := rows.Scan(&ci.CID, &ci.Name, &ci.Type, &notnull, &dflt, &ci.PK); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		ci.NotNull = notnull != 0
		if dflt.Valid {
			ci.Default = dflt.String
		}
		cols = append(cols, ci)
	}
	if err := rows.Err(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return jsonResult(map[string]any{
		"table":   exists,
		"columns": cols,
	})
}

// quoteIdent wraps a SQL identifier in double quotes, escaping any embedded
// quote by doubling it.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// jsonResult marshals v and wraps it as an MCP text result.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
