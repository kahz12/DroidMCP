package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kahz12/droidmcp/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

// fakeClock is a settable time source for the limiter.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(limits map[string]config.RateLimit) (*limiter, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	return newLimiter(limits, clock.now), clock
}

func TestLimiterSlidingWindowIsExact(t *testing.T) {
	l, clock := newTestLimiter(map[string]config.RateLimit{"send": {Calls: 3, Per: time.Minute}})

	for i := 0; i < 3; i++ {
		if ok, _, _ := l.admit("send"); !ok {
			t.Fatalf("call %d within the limit was refused", i+1)
		}
		clock.advance(time.Second)
	}
	// Three calls at 0s, 1s and 2s: the fourth, at 3s, waits until the first
	// leaves the window at 60s.
	ok, scope, wait := l.admit("send")
	if ok || scope != "send" || wait != 57*time.Second {
		t.Fatalf("4th call: ok=%v scope=%q wait=%v, want refused by send for 57s", ok, scope, wait)
	}
	clock.advance(57 * time.Second)
	if ok, _, _ := l.admit("send"); !ok {
		t.Fatal("a slot should be free once the oldest call left the window")
	}
	// The window slides: the calls from 1s and 2s still count, so there is
	// no fresh burst of three.
	if ok, _, _ := l.admit("send"); ok {
		t.Fatal("the window must not reset as a whole")
	}
	// Other tools are not limited.
	if ok, _, _ := l.admit("other"); !ok {
		t.Fatal("a tool without a limit was refused")
	}
}

func TestLimiterServerWideLimitCountsEveryTool(t *testing.T) {
	l, _ := newTestLimiter(map[string]config.RateLimit{config.AllTools: {Calls: 2, Per: time.Minute}})
	l.admit("a")
	l.admit("b")
	if ok, scope, _ := l.admit("c"); ok || scope != config.AllTools {
		t.Fatalf("third call to any tool: ok=%v scope=%q, want refused by %q", ok, scope, config.AllTools)
	}
}

// A refused call consumes nothing, in either window.
func TestLimiterRefusedCallsAreNotRecorded(t *testing.T) {
	l, _ := newTestLimiter(map[string]config.RateLimit{
		"send":          {Calls: 1, Per: time.Hour},
		config.AllTools: {Calls: 2, Per: time.Minute},
	})
	l.admit("send")
	for i := 0; i < 5; i++ {
		if ok, _, _ := l.admit("send"); ok {
			t.Fatal("send is over its own limit")
		}
	}
	// The refused sends did not use the server-wide budget: one slot is left.
	if ok, _, _ := l.admit("read"); !ok {
		t.Fatal("refused calls ate into the server-wide limit")
	}
	if ok, _, _ := l.admit("read"); ok {
		t.Fatal("server-wide limit of 2 should be full now")
	}

	// And a call refused by the server-wide window leaves the tool's own
	// window untouched.
	l2, clock2 := newTestLimiter(map[string]config.RateLimit{
		"send":          {Calls: 1, Per: time.Hour},
		config.AllTools: {Calls: 1, Per: time.Minute},
	})
	l2.admit("read")
	if ok, scope, _ := l2.admit("send"); ok || scope != config.AllTools {
		t.Fatalf("send should be refused by the server-wide limit, got ok=%v scope=%q", ok, scope)
	}
	clock2.advance(time.Minute)
	if ok, _, _ := l2.admit("send"); !ok {
		t.Fatal("send's own slot was used up by a call that never ran")
	}
}

func TestLimiterMiddlewareRefusesWithReadableError(t *testing.T) {
	l, _ := newTestLimiter(map[string]config.RateLimit{"send": {Calls: 1, Per: time.Hour}})
	calls := 0
	h := l.middleware(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls++
		return mcp.NewToolResultText("sent"), nil
	})
	var req mcp.CallToolRequest
	req.Params.Name = "send"

	if res, err := h(context.Background(), req); err != nil || res.IsError {
		t.Fatalf("first call: %+v %v", res, err)
	}
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("a refusal is a tool result, not a protocol error: %v", err)
	}
	if !res.IsError || calls != 1 {
		t.Fatalf("second call should be refused without running: isError=%v calls=%d", res.IsError, calls)
	}
	text := res.Content[0].(mcp.TextContent).Text
	for _, want := range []string{`"send"`, "1 call per hour", "retry in 1h0m0s"} {
		if !strings.Contains(text, want) {
			t.Errorf("message %q should contain %q", text, want)
		}
	}
}

func TestApplyRateLimitsRejectsUnknownTools(t *testing.T) {
	hourly := config.RateLimit{Calls: 10, Per: time.Hour}

	t.Run("unknown operator name", func(t *testing.T) {
		ds := newEchoServer("")
		err := ds.applyRateLimits(&config.Config{Server: "sms",
			RateLimits: map[string]config.RateLimit{"sendsms": hourly}})
		if err == nil {
			t.Fatal("a misspelled tool must stop the server")
		}
		for _, want := range []string{"DROIDMCP_SMS_RATE_LIMITS", "sendsms", "has: echo"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should mention %q: %v", want, err)
			}
		}
	})

	t.Run("unknown default name", func(t *testing.T) {
		ds := newEchoServer("")
		ds.DefaultRateLimits = map[string]config.RateLimit{"renamed_tool": hourly}
		if err := ds.applyRateLimits(&config.Config{Server: "sms"}); err == nil {
			t.Fatal("a built-in limit on a missing tool must not be dropped silently")
		}
	})
}

// callEchoTwice calls echo once in each of two stdio sessions on ds — they share
// its limiter — and returns each session's raw output.
func callEchoTwice(t *testing.T, ds *DroidServer) [2]string {
	t.Helper()
	call := func(id string) string {
		return `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hola"}}}`
	}
	in := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + mcp.LATEST_PROTOCOL_VERSION + `","capabilities":{},"clientInfo":{"name":"core-test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		call("2"),
	}, "\n") + "\n")
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ds.listenStdio(ctx, in, &out); err != nil {
		t.Fatal(err)
	}
	// A second session on the same server shares the limiter.
	in2 := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + mcp.LATEST_PROTOCOL_VERSION + `","capabilities":{},"clientInfo":{"name":"core-test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		call("3"),
	}, "\n") + "\n")
	var out2 syncBuffer
	if err := ds.listenStdio(ctx, in2, &out2); err != nil {
		t.Fatal(err)
	}
	return [2]string{out.String(), out2.String()}
}

func TestRateLimitEndToEnd(t *testing.T) {
	t.Run("default applies", func(t *testing.T) {
		ds := newEchoServer("")
		ds.DefaultRateLimits = map[string]config.RateLimit{"echo": {Calls: 1, Per: time.Hour}}
		if err := ds.applyRateLimits(&config.Config{Server: "sms"}); err != nil {
			t.Fatal(err)
		}
		got := callEchoTwice(t, ds)
		if !strings.Contains(got[0], `"text":"hola"`) {
			t.Errorf("first call should run: %s", got[0])
		}
		if !strings.Contains(got[1], "rate limit") || !strings.Contains(got[1], `"isError":true`) {
			t.Errorf("second call should be refused: %s", got[1])
		}
	})

	t.Run("operator switches the default off", func(t *testing.T) {
		ds := newEchoServer("")
		ds.DefaultRateLimits = map[string]config.RateLimit{"echo": {Calls: 1, Per: time.Hour}}
		if err := ds.applyRateLimits(&config.Config{Server: "sms",
			RateLimits: map[string]config.RateLimit{"echo": {}}}); err != nil {
			t.Fatal(err)
		}
		got := callEchoTwice(t, ds)
		if strings.Contains(got[1], "rate limit") {
			t.Errorf("echo=off should remove the default: %s", got[1])
		}
	})

	t.Run("operator tightens the default", func(t *testing.T) {
		ds := newEchoServer("")
		ds.DefaultRateLimits = map[string]config.RateLimit{"echo": {Calls: 100, Per: time.Hour}}
		if err := ds.applyRateLimits(&config.Config{Server: "sms",
			RateLimits: map[string]config.RateLimit{"echo": {Calls: 1, Per: time.Hour}}}); err != nil {
			t.Fatal(err)
		}
		if got := callEchoTwice(t, ds); !strings.Contains(got[1], "rate limit") {
			t.Errorf("the operator's 1/h should replace the default 100/h: %s", got[1])
		}
	})
}
