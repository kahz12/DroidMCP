package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kahz12/droidmcp/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

// callRequest builds an mcp.CallToolRequest with the given arguments map.
func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	}
}

// resultText concatenates all text-content blocks and reports the IsError flag.
func resultText(t *testing.T, res *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if res == nil {
		t.Fatalf("expected non-nil result")
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// withRoot points the global cfg at a fresh temp directory and clears the
// connection cache so each test is isolated.
func withRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcp-sqlite-test")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		dbMu.Lock()
		for k, db := range dbCache {
			_ = db.Close()
			delete(dbCache, k)
		}
		dbMu.Unlock()
		os.RemoveAll(dir)
	})
	dbMu.Lock()
	for k, db := range dbCache {
		_ = db.Close()
		delete(dbCache, k)
	}
	dbMu.Unlock()
	cfg = &config.Config{Root: dir}
	return dir
}

// mustCall invokes a handler and returns its result, failing on a Go-level error.
func mustCall(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := h(context.Background(), callRequest(args))
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	return res
}

// okText runs a handler, asserts success, and returns the text payload.
func okText(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) string {
	t.Helper()
	got, isErr := resultText(t, mustCall(t, h, args))
	if isErr {
		t.Fatalf("unexpected error result: %s", got)
	}
	return got
}

// seed opens a database and creates a small users table with two rows.
func seed(t *testing.T, db string) {
	t.Helper()
	okText(t, handleOpenDB, map[string]any{"db": db})
	okText(t, handleExecute, map[string]any{
		"db":  db,
		"sql": "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER)",
	})
	okText(t, handleExecute, map[string]any{
		"db":   db,
		"sql":  "INSERT INTO users (name, age) VALUES (?, ?), (?, ?)",
		"args": []any{"alice", 30.0, "bob", 25.0},
	})
}

func TestSecurePath(t *testing.T) {
	root := withRoot(t)
	cases := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{"valid", "app.db", false},
		{"nested", "data/app.db", false},
		{"escape", "../out.db", true},
		{"absolute", "/etc/passwd", true},
		{"dotdot", "sub/../../out.db", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := securePath(tc.rel)
			if (err != nil) != tc.wantErr {
				t.Fatalf("securePath(%q) err=%v wantErr=%v", tc.rel, err, tc.wantErr)
			}
			if !tc.wantErr {
				absRoot, _ := filepath.Abs(root)
				if !strings.HasPrefix(got, absRoot) {
					t.Errorf("securePath(%q)=%q, want prefix %q", tc.rel, got, absRoot)
				}
			}
		})
	}
}

func TestLeadingKeyword(t *testing.T) {
	cases := map[string]string{
		"SELECT * FROM t":      "SELECT",
		"  select 1":           "SELECT",
		"\nWITH x AS (…)":      "WITH",
		"insert into t":        "INSERT",
		"(SELECT 1)":           "SELECT",
		"PRAGMA table_info(t)": "PRAGMA",
		"":                     "",
		"DELETE FROM t":        "DELETE",
	}
	for in, want := range cases {
		if got := leadingKeyword(in); got != want {
			t.Errorf("leadingKeyword(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestNormalizeArg(t *testing.T) {
	if got := normalizeArg(5.0); got != int64(5) {
		t.Errorf("integral float should become int64, got %T(%v)", got, got)
	}
	if got := normalizeArg(2.5); got != 2.5 {
		t.Errorf("non-integral float should stay float64, got %T(%v)", got, got)
	}
	if got := normalizeArg("x"); got != "x" {
		t.Errorf("string should pass through, got %v", got)
	}
	if got := normalizeArg(nil); got != nil {
		t.Errorf("nil should pass through, got %v", got)
	}
}

func TestNormalizeValueAndCSVCell(t *testing.T) {
	if got := normalizeValue([]byte("hi")); got != "hi" {
		t.Errorf("[]byte should become string, got %v", got)
	}
	if got := normalizeValue(int64(7)); got != int64(7) {
		t.Errorf("int64 should pass through, got %v", got)
	}
	cells := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"txt", "txt"},
		{true, "true"},
		{int64(9), "9"},
		{[]byte("ab"), "ab"},
	}
	for _, c := range cells {
		if got := csvCell(c.in); got != c.want {
			t.Errorf("csvCell(%v)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	if got := quoteIdent("users"); got != `"users"` {
		t.Errorf(`quoteIdent("users")=%q`, got)
	}
	if got := quoteIdent(`a"b`); got != `"a""b"` {
		t.Errorf("embedded quote not escaped: %q", got)
	}
}

func TestOpenDBCreatesFile(t *testing.T) {
	root := withRoot(t)
	got := okText(t, handleOpenDB, map[string]any{"db": "data/app.db"})

	var out map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out["created"] != true {
		t.Errorf("expected created=true, got %v", out["created"])
	}
	if v, _ := out["sqlite_version"].(string); v == "" {
		t.Errorf("expected a sqlite_version, got %v", out["sqlite_version"])
	}
	if _, err := os.Stat(filepath.Join(root, "data", "app.db")); err != nil {
		t.Errorf("database file not created: %v", err)
	}

	// A second open reports created=false.
	got2 := okText(t, handleOpenDB, map[string]any{"db": "data/app.db"})
	var out2 map[string]any
	_ = json.Unmarshal([]byte(got2), &out2)
	if out2["created"] != false {
		t.Errorf("second open should report created=false, got %v", out2["created"])
	}
}

func TestExecuteAndQuery(t *testing.T) {
	withRoot(t)
	seed(t, "app.db")

	got := okText(t, handleQuery, map[string]any{
		"db":   "app.db",
		"sql":  "SELECT id, name, age FROM users WHERE age >= ? ORDER BY id",
		"args": []any{26.0},
	})
	var res struct {
		Columns   []string         `json:"columns"`
		Rows      []map[string]any `json:"rows"`
		Count     int              `json:"count"`
		Truncated bool             `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(got), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if res.Count != 1 || len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %s", res.Count, got)
	}
	if res.Rows[0]["name"] != "alice" {
		t.Errorf("expected alice, got %v", res.Rows[0]["name"])
	}
	if len(res.Columns) != 3 || res.Columns[1] != "name" {
		t.Errorf("unexpected columns: %v", res.Columns)
	}
}

func TestQueryRejectsWrite(t *testing.T) {
	withRoot(t)
	seed(t, "app.db")
	_, isErr := resultText(t, mustCall(t, handleQuery, map[string]any{
		"db":  "app.db",
		"sql": "DELETE FROM users",
	}))
	if !isErr {
		t.Fatalf("query should reject a DELETE statement")
	}
	// The row count must be unchanged.
	got := okText(t, handleQuery, map[string]any{"db": "app.db", "sql": "SELECT count(*) AS n FROM users"})
	if !strings.Contains(got, `"n":2`) {
		t.Errorf("rows should be untouched, got %s", got)
	}
}

func TestQueryMaxRowsTruncates(t *testing.T) {
	withRoot(t)
	seed(t, "app.db")
	okText(t, handleExecute, map[string]any{
		"db": "app.db", "sql": "INSERT INTO users (name, age) VALUES ('carol', 40)",
	})
	got := okText(t, handleQuery, map[string]any{
		"db": "app.db", "sql": "SELECT * FROM users", "max_rows": 2.0,
	})
	var res struct {
		Count     int  `json:"count"`
		Truncated bool `json:"truncated"`
	}
	_ = json.Unmarshal([]byte(got), &res)
	if res.Count != 2 || !res.Truncated {
		t.Errorf("expected count=2 truncated=true, got %+v (%s)", res, got)
	}
}

func TestParameterizedArgsPreventInjection(t *testing.T) {
	withRoot(t)
	seed(t, "app.db")
	// A classic injection payload passed as a *value* must be stored literally,
	// not executed. The users table must survive.
	payload := "x'); DROP TABLE users;--"
	okText(t, handleExecute, map[string]any{
		"db": "app.db", "sql": "INSERT INTO users (name) VALUES (?)", "args": []any{payload},
	})
	got := okText(t, handleQuery, map[string]any{
		"db": "app.db", "sql": "SELECT name FROM users WHERE name = ?", "args": []any{payload},
	})
	if !strings.Contains(got, `"count":1`) {
		t.Fatalf("payload should be stored verbatim as one row, got %s", got)
	}
	// The table still exists (describe succeeds).
	if _, isErr := resultText(t, mustCall(t, handleDescribeTable, map[string]any{"db": "app.db", "table": "users"})); isErr {
		t.Errorf("users table should still exist after injection attempt")
	}
}

func TestListTables(t *testing.T) {
	withRoot(t)
	seed(t, "app.db")
	okText(t, handleExecute, map[string]any{
		"db": "app.db", "sql": "CREATE VIEW adults AS SELECT * FROM users WHERE age >= 18",
	})
	got := okText(t, handleListTables, map[string]any{"db": "app.db"})
	var tables []tableRow
	if err := json.Unmarshal([]byte(got), &tables); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	seen := map[string]string{}
	for _, tr := range tables {
		seen[tr.Name] = tr.Type
	}
	if seen["users"] != "table" || seen["adults"] != "view" {
		t.Errorf("unexpected tables: %+v", tables)
	}
	if _, ok := seen["sqlite_sequence"]; ok {
		t.Errorf("internal sqlite_* objects should be excluded: %+v", tables)
	}
}

func TestDescribeTable(t *testing.T) {
	withRoot(t)
	seed(t, "app.db")
	got := okText(t, handleDescribeTable, map[string]any{"db": "app.db", "table": "users"})
	var out struct {
		Table   string       `json:"table"`
		Columns []columnInfo `json:"columns"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Table != "users" || len(out.Columns) != 3 {
		t.Fatalf("unexpected schema: %+v", out)
	}
	byName := map[string]columnInfo{}
	for _, c := range out.Columns {
		byName[c.Name] = c
	}
	if byName["id"].PK != 1 {
		t.Errorf("id should be primary key: %+v", byName["id"])
	}
	if !byName["name"].NotNull {
		t.Errorf("name should be NOT NULL: %+v", byName["name"])
	}
}

func TestDescribeMissingTable(t *testing.T) {
	withRoot(t)
	seed(t, "app.db")
	_, isErr := resultText(t, mustCall(t, handleDescribeTable, map[string]any{"db": "app.db", "table": "nope"}))
	if !isErr {
		t.Fatalf("expected error for missing table")
	}
}

func TestMissingDatabaseRejected(t *testing.T) {
	withRoot(t)
	_, isErr := resultText(t, mustCall(t, handleQuery, map[string]any{"db": "ghost.db", "sql": "SELECT 1"}))
	if !isErr {
		t.Fatalf("query on a missing database should error (only open_db creates)")
	}
}

func TestExportCSV(t *testing.T) {
	root := withRoot(t)
	seed(t, "app.db")
	got := okText(t, handleExportCSV, map[string]any{
		"db":          "app.db",
		"sql":         "SELECT name, age FROM users ORDER BY name",
		"destination": "out/users.csv",
	})
	var out struct {
		Path    string   `json:"path"`
		Rows    int      `json:"rows"`
		Columns []string `json:"columns"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Rows != 2 {
		t.Errorf("expected 2 rows, got %d", out.Rows)
	}
	data, err := os.ReadFile(filepath.Join(root, "out", "users.csv"))
	if err != nil {
		t.Fatalf("csv not written: %v", err)
	}
	want := "name,age\nalice,30\nbob,25\n"
	if string(data) != want {
		t.Errorf("csv mismatch:\n got %q\nwant %q", string(data), want)
	}
}

func TestExportCSVRejectsWriteAndSelfOverwrite(t *testing.T) {
	withRoot(t)
	seed(t, "app.db")
	if _, isErr := resultText(t, mustCall(t, handleExportCSV, map[string]any{
		"db": "app.db", "sql": "DELETE FROM users", "destination": "out.csv",
	})); !isErr {
		t.Errorf("export_csv should reject a non-read statement")
	}
	if _, isErr := resultText(t, mustCall(t, handleExportCSV, map[string]any{
		"db": "app.db", "sql": "SELECT 1", "destination": "app.db",
	})); !isErr {
		t.Errorf("export_csv should refuse to overwrite the source database")
	}
}
