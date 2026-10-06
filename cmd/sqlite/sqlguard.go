package main

import (
	"errors"
	"strings"
)

// errFileAccessSQL is returned for statements that make SQLite open or create a
// file whose path comes from the SQL text itself. Every other path in this
// server goes through securePath, so these statements would bypass
// DROIDMCP_ROOT.
var errFileAccessSQL = errors.New("ATTACH and VACUUM INTO are not allowed: they read or write files outside the root confinement")

// rejectFileAccessSQL scans every statement in sqlText and rejects ATTACH and
// VACUUM ... INTO. It applies to query and export_csv as well as execute: the
// driver runs stacked statements even on a mode=ro handle, and ATTACH can read
// any database file while VACUUM INTO can write a copy of the database
// anywhere.
//
// The scan follows SQLite's own lexing (quoted strings and identifiers,
// comments, ';' separators, a NUL ending the text) so a keyword hidden after a
// comment or a quoted ';' is still found at the start of its statement.
//
// This is deliberately a statement scan and not SQLITE_LIMIT_ATTACHED=0 on the
// connection: SQLite implements every VACUUM (with or without INTO) through an
// internal ATTACH, so that limit would also break plain VACUUM maintenance.
func rejectFileAccessSQL(sqlText string) error {
	var first string // first word of the current statement
	sawInto := false // VACUUM statement has an INTO clause

	endStatement := func() error {
		if first == "VACUUM" && sawInto {
			return errFileAccessSQL
		}
		first, sawInto = "", false
		return nil
	}
	onWord := func(w string) error {
		if first == "" {
			first = w
			if first == "ATTACH" {
				return errFileAccessSQL
			}
			return nil
		}
		if first == "VACUUM" && w == "INTO" {
			sawInto = true
		}
		return nil
	}

	s := sqlText
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0:
			// SQLite stops at the first NUL byte.
			return endStatement()
		case c == ';':
			if err := endStatement(); err != nil {
				return err
			}
			i++
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return endStatement()
			}
			i += 2 + end + 2
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(s, i, c)
			if first == "" {
				// A quoted token cannot be a keyword; mark the statement as
				// started so a later ATTACH in the same statement is not
				// mistaken for its first word.
				first = "\x00"
			}
		case c == '[':
			end := strings.IndexByte(s[i+1:], ']')
			if end < 0 {
				return endStatement()
			}
			i += end + 2
			if first == "" {
				first = "\x00"
			}
		case isWordByte(c):
			j := i
			for j < len(s) && isWordByte(s[j]) {
				j++
			}
			if err := onWord(strings.ToUpper(s[i:j])); err != nil {
				return err
			}
			i = j
		default:
			i++
		}
	}
	return endStatement()
}

// skipQuoted returns the index just past a quoted string or identifier that
// starts at s[i], where a doubled quote character is an escaped quote.
func skipQuoted(s string, i int, quote byte) int {
	for i++; i < len(s); i++ {
		if s[i] != quote {
			continue
		}
		if i+1 < len(s) && s[i+1] == quote {
			i++
			continue
		}
		return i + 1
	}
	return len(s)
}

// isWordByte reports whether b can be part of an SQL keyword or bare
// identifier. Bytes >= 0x80 are treated as identifier characters, as SQLite
// does for UTF-8.
func isWordByte(b byte) bool {
	return b == '_' || b == '$' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b >= 0x80
}
