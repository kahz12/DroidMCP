package main

import (
	"database/sql"
	"testing"
)

func BenchmarkScanRows(b *testing.B) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	const query = `WITH RECURSIVE numbers(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM numbers WHERE n<1000) SELECT n, 'value' AS text FROM numbers`
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rows, err := db.Query(query)
		if err != nil {
			b.Fatal(err)
		}
		_, data, _, err := scanRows(rows, 0)
		rows.Close()
		if err != nil {
			b.Fatal(err)
		}
		if len(data) != 1000 {
			b.Fatalf("unexpected rows: %d", len(data))
		}
	}
}
