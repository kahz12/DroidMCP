package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kahz12/droidmcp/internal/config"
)

func TestSecurePath(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "mcp-test")
	defer os.RemoveAll(tmpDir)

	cfg = &config.Config{
		Root: tmpDir,
	}

	tests := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{"valid path", "test.txt", false},
		{"nested path", "subdir/test.txt", false},
		{"escape attempt", "../outside.txt", true},
		{"absolute escape", "/etc/passwd", true},
		{"dot dot slash", "subdir/../../outside.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := securePath(tt.rel)
			if (err != nil) != tt.wantErr {
				t.Errorf("securePath(%s) error = %v, wantErr %v", tt.rel, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				absRoot, _ := filepath.Abs(tmpDir)
				if !filepath.HasPrefix(got, absRoot) {
					t.Errorf("securePath(%s) = %s, does not have prefix %s", tt.rel, got, absRoot)
				}
			}
		})
	}
}
