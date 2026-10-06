package core

import (
	"strconv"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestTimeoutArg(t *testing.T) {
	const def, max = 5 * time.Second, 60 * time.Second
	cases := []struct {
		name string
		args map[string]any
		want time.Duration
	}{
		{"absent uses the default", nil, def},
		{"zero uses the default", map[string]any{"timeout_seconds": 0.0}, def},
		{"negative uses the default", map[string]any{"timeout_seconds": -3.0}, def},
		{"in range is honoured", map[string]any{"timeout_seconds": 30.0}, 30 * time.Second},
		{"the cap itself is honoured", map[string]any{"timeout_seconds": 60.0}, max},
		{"above the cap is clamped", map[string]any{"timeout_seconds": 9999.0}, max},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: tc.args}}
			if got := TimeoutArg(req, def, max); got != tc.want {
				t.Fatalf("TimeoutArg = %v, want %v", got, tc.want)
			}
		})
	}

	req := mcp.CallToolRequest{}
	if got := TimeoutArg(req, 0, max); got != 0 {
		t.Fatalf("zero default = %v, want 0", got)
	}
}

// Values whose conversion to nanoseconds overflows int64 must still hit the cap
// (1<<35 s * 1e9 wraps to a small value, 1<<34 s to a negative one).
func TestTimeoutArgOverflowHitsTheCap(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("the overflow values do not fit a 32-bit int")
	}
	const def, max = 5 * time.Second, 60 * time.Second
	for _, secs := range []float64{1 << 33, 1 << 34, 1 << 35, 1 << 40, 9.3e12} {
		req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"timeout_seconds": secs}}}
		if got := TimeoutArg(req, def, max); got != max {
			t.Errorf("timeout_seconds=%g gave %v, want the %v cap", secs, got, max)
		}
	}
}
