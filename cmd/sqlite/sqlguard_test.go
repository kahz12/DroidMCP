package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectFileAccessSQL(t *testing.T) {
	rejected := []string{
		"ATTACH DATABASE '/tmp/x.db' AS o",
		"  attach '/tmp/x.db' as o",
		"SELECT 1; ATTACH DATABASE '/tmp/x.db' AS o",
		"/* hi */ ATTACH '/tmp/x.db' AS o",
		"-- note\nATTACH '/tmp/x.db' AS o",
		"SELECT ';'; ATTACH '/tmp/x.db' AS o",
		"SELECT \"a;b\"; ATTACH '/tmp/x.db' AS o",
		"SELECT 'it''s'; ATTACH '/tmp/x.db' AS o",
		"SELECT [a;b] FROM t; ATTACH '/tmp/x.db' AS o",
		"SELECT `a;b` FROM t;ATTACH '/tmp/x.db' AS o",
		"\nATTACH\n'/tmp/x.db'\nAS o",
		"VACUUM INTO '/tmp/copy.db'",
		"vacuum main into '/tmp/copy.db'",
		"SELECT 1; VACUUM INTO ?",
		"VACUUM /* c */ INTO '/tmp/copy.db'",
		";;ATTACH '/tmp/x.db' AS o",
		"SELECT 1 -- ;\n; ATTACH '/tmp/x.db' AS o",
		"VACUUM INTO '/tmp/copy.db'; SELECT 1",
		"VACUUM INTO '/tmp/copy.db' /* unterminated",
		"VACUUM INTO '/tmp/copy.db' [unterminated",
		"VACUUM [main] INTO '/tmp/copy.db'",
		"VACUUM \"main\" INTO '/tmp/copy.db'",
		"[t]; ATTACH '/tmp/x.db' AS o",
		"'t'; ATTACH '/tmp/x.db' AS o",
	}
	for _, sql := range rejected {
		if err := rejectFileAccessSQL(sql); err == nil {
			t.Errorf("accepted %q", sql)
		}
	}

	allowed := []string{
		"SELECT 1",
		"SELECT 'ATTACH DATABASE x' AS s",
		"SELECT \"ATTACH\" FROM t",
		"INSERT INTO notes(body) VALUES ('VACUUM INTO x')",
		"VACUUM",
		"VACUUM main",
		"-- ATTACH x\nSELECT 1",
		"/* ATTACH x */ SELECT 1",
		"SELECT 1; SELECT 2",
		"CREATE TABLE attach_log(vacuum_into TEXT)",
		"SELECT attach FROM t",
		"EXPLAIN ATTACH '/tmp/x.db' AS o",
		"",
		"SELECT 1;\x00ATTACH '/tmp/x.db' AS o",
	}
	for _, sql := range allowed {
		if err := rejectFileAccessSQL(sql); err != nil {
			t.Errorf("rejected %q: %v", sql, err)
		}
	}
}

// The driver runs stacked statements even on a read-only handle, so every tool
// that takes SQL must refuse ATTACH / VACUUM INTO.
func TestSQLToolsCannotReachFilesOutsideRoot(t *testing.T) {
	root := withRoot(t)
	seed(t, "app.db")
	outside := filepath.Join(t.TempDir(), "outside.db")
	copyPath := filepath.Join(t.TempDir(), "copy.db")

	calls := map[string]func(sql string) (string, bool){
		"query": func(sql string) (string, bool) {
			return resultText(t, mustCall(t, handleQuery, map[string]any{"db": "app.db", "sql": sql}))
		},
		"execute": func(sql string) (string, bool) {
			return resultText(t, mustCall(t, handleExecute, map[string]any{"db": "app.db", "sql": sql}))
		},
		"export_csv": func(sql string) (string, bool) {
			return resultText(t, mustCall(t, handleExportCSV, map[string]any{"db": "app.db", "destination": "out.csv", "sql": sql}))
		},
	}
	for name, call := range calls {
		for _, sql := range []string{
			"SELECT 1; ATTACH DATABASE '" + outside + "' AS o",
			"SELECT 1; VACUUM INTO '" + copyPath + "'",
		} {
			if name == "execute" {
				sql = sql[len("SELECT 1; "):]
			}
			if _, failed := call(sql); !failed {
				t.Errorf("%s accepted %q", name, sql)
			}
		}
	}
	for _, path := range []string{outside, copyPath, filepath.Join(root, "out.csv")} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s was created", path)
		}
	}
}

// Plain VACUUM (no INTO) is ordinary maintenance and must keep working.
func TestExecuteAllowsPlainVacuum(t *testing.T) {
	withRoot(t)
	seed(t, "app.db")
	okText(t, handleExecute, map[string]any{"db": "app.db", "sql": "VACUUM"})
}
