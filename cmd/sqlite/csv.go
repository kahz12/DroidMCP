package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
)

func handleExportCSV(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, err := req.RequireString("db")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sqlText, err := req.RequireString("sql")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if kw := leadingKeyword(sqlText); !readKeywords[kw] {
		return mcp.NewToolResultError(fmt.Sprintf("export_csv only runs read statements (SELECT/WITH/PRAGMA/EXPLAIN/VALUES); got %q", kw)), nil
	}
	destRel, err := req.RequireString("destination")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	args, err := toolArgs(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Resolve both paths and refuse to overwrite the source database with its own
	// export.
	dbAbs, err := securePath(rel)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	destAbs, err := securePath(destRel)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if destAbs == dbAbs {
		return mcp.NewToolResultError("destination must differ from the database file"), nil
	}
	if info, statErr := os.Stat(destAbs); statErr == nil && info.IsDir() {
		return mcp.NewToolResultError(fmt.Sprintf("destination %q is a directory", destRel)), nil
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

	cols, err := rows.Columns()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Track whether the destination already existed so a failure only removes a
	// file this call created — never pre-existing user data.
	preExisting := false
	if _, statErr := os.Stat(destAbs); statErr == nil {
		preExisting = true
	}
	f, err := os.Create(destAbs)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	written, writeErr := streamCSV(f, rows, cols)
	closeErr := f.Close()

	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = rows.Err()
	}
	if writeErr != nil {
		if !preExisting {
			_ = os.Remove(destAbs)
		}
		return mcp.NewToolResultError(writeErr.Error()), nil
	}

	return jsonResult(map[string]any{
		"path":    destRel,
		"rows":    written,
		"columns": cols,
	})
}

// streamCSV writes a header row followed by one CSV record per result row,
// returning the number of data rows written. Values are rendered with csvCell.
func streamCSV(f *os.File, rows rowScanner, cols []string) (int, error) {
	w := csv.NewWriter(f)
	if err := w.Write(cols); err != nil {
		return 0, err
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	rec := make([]string, len(cols))
	written := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return written, err
		}
		for i := range vals {
			rec[i] = csvCell(vals[i])
		}
		if err := w.Write(rec); err != nil {
			return written, err
		}
		written++
	}
	w.Flush()
	return written, w.Error()
}

// rowScanner is the subset of *sql.Rows streamCSV needs, kept as an interface so
// the writer can be unit-tested without a live database.
type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
}

// csvCell renders a scanned SQL value as a CSV field. NULL becomes an empty
// field; TEXT/BLOB ([]byte) and strings pass through; everything else uses its
// default string form.
func csvCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
