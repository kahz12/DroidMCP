package core

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// applyRateLimits merges the server's built-in DefaultRateLimits with the
// operator's DROIDMCP_<SERVER>_RATE_LIMITS (which win, and can switch a
// default off) and, if any limit is left, installs the limiter as tool
// middleware — so it covers every transport, stdio included. It runs before
// disableTools: names are checked against every registered tool, and a limit
// on a tool that is then disabled simply never fires.
func (s *DroidServer) applyRateLimits(cfg *config.Config) error {
	limits := make(map[string]config.RateLimit, len(s.DefaultRateLimits)+len(cfg.RateLimits))
	for name, l := range s.DefaultRateLimits {
		if name != config.AllTools && s.MCPServer.GetTool(name) == nil {
			return fmt.Errorf("%s: built-in rate limit names unknown tool %q", s.Name, name)
		}
		limits[name] = l
	}
	var unknown []string
	for name, l := range cfg.RateLimits {
		if name != config.AllTools && s.MCPServer.GetTool(name) == nil {
			unknown = append(unknown, name)
			continue
		}
		if l.Off() {
			delete(limits, name)
		} else {
			limits[name] = l
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("%s names unknown tool(s) %s; %s has: %s (or %q for the whole server)",
			config.RateLimitsVar(cfg.Server), strings.Join(unknown, ", "), s.Name,
			strings.Join(s.toolNames(), ", "), config.AllTools)
	}
	if len(limits) == 0 {
		return nil
	}

	l := newLimiter(limits, time.Now)
	s.MCPServer.Use(l.middleware)
	logger.Info("Rate limits", "limits", l.String())
	return nil
}

// toolNames lists the registered tools, sorted, for error messages.
func (s *DroidServer) toolNames() []string {
	registered := s.MCPServer.ListTools()
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// limiter enforces per-tool and server-wide (config.AllTools) rate limits on
// tool calls. One mutex guards every window: calls are counted, not timed, so
// contention is negligible, and a single lock lets admit check the tool's
// window and the server-wide one together.
type limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	windows map[string]*window
}

func newLimiter(limits map[string]config.RateLimit, now func() time.Time) *limiter {
	l := &limiter{now: now, windows: make(map[string]*window, len(limits))}
	for name, limit := range limits {
		l.windows[name] = &window{limit: limit}
	}
	return l
}

// admit records a call to tool unless its own window or the server-wide one is
// full. A refused call is not recorded, so retrying after the reported wait
// succeeds. It returns the scope that refused ("*" or the tool name) and how
// long until that window frees a slot.
func (l *limiter) admit(tool string) (ok bool, scope string, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	applicable := make([]string, 0, 2)
	for _, name := range []string{tool, config.AllTools} {
		if w, found := l.windows[name]; found {
			if d := w.wait(now); d > 0 {
				return false, name, d
			}
			applicable = append(applicable, name)
		}
	}
	for _, name := range applicable {
		l.windows[name].record(now)
	}
	return true, "", 0
}

// middleware refuses a tool call over its limit with an error result the
// calling model can read, rather than a protocol error, so it can wait and
// retry instead of treating the server as broken.
func (l *limiter) middleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tool := req.Params.Name
		ok, scope, wait := l.admit(tool)
		if ok {
			return next(ctx, req)
		}
		limit := l.windows[scope].limit
		retry := time.Duration(math.Ceil(wait.Seconds())) * time.Second
		logger.Log.Warn("rate limited", "tool", tool, "scope", scope, "limit", limit.String(), "retry_in", retry.String())
		what := fmt.Sprintf("%q", tool)
		if scope == config.AllTools {
			what = "this server"
		}
		calls := "calls"
		if limit.Calls == 1 {
			calls = "call"
		}
		return mcp.NewToolResultError(fmt.Sprintf(
			"rate limit: %s allows %d %s per %s; retry in %s",
			what, limit.Calls, calls, unitName(limit.Per), retry)), nil
	}
}

// String lists the limits as "name=N/unit", sorted, for the startup log.
func (l *limiter) String() string {
	parts := make([]string, 0, len(l.windows))
	for name, w := range l.windows {
		parts = append(parts, name+"="+w.limit.String())
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// unitName spells out a limit's period for the error message.
func unitName(d time.Duration) string {
	switch d {
	case time.Second:
		return "second"
	case time.Minute:
		return "minute"
	case time.Hour:
		return "hour"
	case 24 * time.Hour:
		return "day"
	}
	return d.String()
}

// window is a sliding-window log: the start times of the most recent
// limit.Calls admitted calls, in a ring buffer. A new call fits when fewer
// than limit.Calls calls started in the last limit.Per — exact, unlike a token
// bucket, which can let through up to twice the limit across a window.
type window struct {
	limit config.RateLimit
	times []time.Time // ring buffer; oldest entry at next once full
	next  int
}

// wait reports how long until the window has room for one more call; zero
// means now.
func (w *window) wait(now time.Time) time.Duration {
	if len(w.times) < w.limit.Calls {
		return 0
	}
	if d := w.times[w.next].Add(w.limit.Per).Sub(now); d > 0 {
		return d
	}
	return 0
}

// record notes a call admitted at now, evicting the oldest once full.
func (w *window) record(now time.Time) {
	if len(w.times) < w.limit.Calls {
		w.times = append(w.times, now)
		return
	}
	w.times[w.next] = now
	w.next = (w.next + 1) % len(w.times)
}
