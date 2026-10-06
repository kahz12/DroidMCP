package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/kahz12/droidmcp/internal/pathsec"
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
	if err := rejectFileAccessSQL(sqlText); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
	// New exports are private (CreateTemp's 0600); an existing destination keeps
	// the permissions it already had.
	destMode := os.FileMode(0o600)
	if info, statErr := os.Stat(destAbs); statErr == nil {
		if info.IsDir() {
			return mcp.NewToolResultError(fmt.Sprintf("destination %q is a directory", destRel)), nil
		}
		if source, err := os.Stat(dbAbs); err == nil && os.SameFile(source, info) {
			return mcp.NewToolResultError("destination must differ from the database file"), nil
		}
		// Publishing by rename only needs write access to the directory, so a
		// read-only destination must be refused explicitly, as an in-place write
		// would have.
		w, err := os.OpenFile(destAbs, os.O_WRONLY, 0)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		_ = w.Close()
		destMode = info.Mode().Perm()
		// Rename replaces a symlink instead of writing through it; publish to the
		// link's target so the link keeps working (securePath already confined it).
		if real, err := pathsec.Resolve(destAbs); err == nil {
			destAbs = real
		}
	}

	// Read-only pool: export runs a SELECT, and mode=ro guarantees a stacked or
	// CTE-hidden write in `sql` cannot mutate the database during the export.
	db, errRes := dbForQuery(rel, true)
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
	// Publish only a complete export. A query/write failure must preserve any
	// previous destination, and a hard link must not truncate another file.
	f, err := os.CreateTemp(filepath.Dir(destAbs), ".droidmcp-export-*.csv")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(f.Name())
		}
	}()

	written, writeErr := streamCSV(f, rows, cols)
	if writeErr == nil {
		writeErr = rows.Err()
	}
	if writeErr == nil {
		writeErr = f.Chmod(destMode)
	}
	// Flush to disk before the rename makes the export visible: otherwise a
	// crash could leave an empty destination with the previous export already
	// gone.
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return mcp.NewToolResultError(writeErr.Error()), nil
	}
	if err := os.Rename(f.Name(), destAbs); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	published = true

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
