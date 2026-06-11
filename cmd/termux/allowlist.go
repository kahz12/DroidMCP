package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// allowlistCheck returns nil if the command is permitted under
// DROIDMCP_TERMUX_ALLOWLIST. An empty/unset env var means "allow all"
// (preserves the prior behaviour). Comparison is on the command's basename
// so callers can pass either "ls" or "/usr/bin/ls".
func allowlistCheck(command string) error {
	raw := strings.TrimSpace(os.Getenv(allowlistEnv))
	if raw == "" {
		return nil
	}
	allowed := parseAllowlist(raw)
	if len(allowed) == 0 {
		return nil
	}
	base := filepath.Base(command)
	if _, ok := allowed[command]; ok {
		return nil
	}
	if _, ok := allowed[base]; ok {
		return nil
	}
	return fmt.Errorf("command %q not in DROIDMCP_TERMUX_ALLOWLIST", command)
}

func parseAllowlist(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}
