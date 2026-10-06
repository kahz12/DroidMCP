package core

import (
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// TimeoutArg reads the optional timeout_seconds argument of a tool call. An
// absent or non-positive value yields def; a value above max yields max.
//
// The clamp compares whole seconds against max/time.Second before multiplying:
// converting first would let a large value wrap time.Duration (int64
// nanoseconds) into a negative or tiny timeout and slip past the cap.
func TimeoutArg(req mcp.CallToolRequest, def, max time.Duration) time.Duration {
	secs := req.GetInt("timeout_seconds", 0)
	if secs <= 0 {
		return def
	}
	if secs > int(max/time.Second) {
		return max
	}
	return time.Duration(secs) * time.Second
}
