// Package config handles environment-based configuration.
// DroidMCP follows a 12-factor approach: every knob is an environment variable
// prefixed with DROIDMCP_. This package owns only the values every server
// shares — Port, Root, Transport, the disabled-tools list and the rate limits —
// and validates them at startup. Server-specific variables (e.g. DROIDMCP_TERMUX_ALLOWLIST,
// DROIDMCP_NETWORK_DB) are read directly with os.Getenv at their point of use,
// keeping a single, predictable configuration idiom across the codebase.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Env prefix and defaults are kept as package constants so tests and callers
// can refer to the same values without restating them.
const (
	envPrefix = "DROIDMCP"

	defaultRoot = "/"

	minPort = 1
	maxPort = 65535
)

// defaultPorts gives every server its own DROIDMCP_PORT default, so several can
// run side by side without any port configuration. The keys are the names each
// cmd/<name> passes to LoadConfig; keep the table in README.md and
// docs/usage.md in sync.
var defaultPorts = map[string]int{
	"filesystem":    3000,
	"github":        3001,
	"scraper":       3002,
	"termux":        3003,
	"network":       3004,
	"clipboard":     3005,
	"media":         3006,
	"sqlite":        3007,
	"sensors":       3008,
	"notifications": 3009,
	"contacts":      3010,
	"sms":           3011,
	"llmproxy":      3012,
	"automation":    3013,
}

// Transports selectable with DROIDMCP_TRANSPORT.
const (
	// TransportHTTP serves MCP on a loopback HTTP listener: Streamable HTTP at
	// /mcp, plus the legacy SSE endpoints (/sse, /message) for older clients.
	TransportHTTP = "http"
	// TransportStdio speaks MCP over stdin/stdout to the process that launched
	// the server. No listener is opened, so Port and the API key are unused.
	TransportStdio = "stdio"
)

// Config holds the configuration values shared by every server: the HTTP Port
// the listener binds to, the filesystem Root (acted on by mcp-filesystem,
// mcp-media and mcp-sqlite), the Transport, the tools the operator switched
// off, and their rate limits. All are validated by LoadConfig.
type Config struct {
	Server        string               // Server name passed to LoadConfig, e.g. "termux"
	Port          int                  // HTTP port for the MCP server (unused with stdio)
	Root          string               // Root directory for filesystem operations
	Transport     string               // TransportHTTP or TransportStdio
	DisabledTools []string             // From DROIDMCP_<SERVER>_DISABLED_TOOLS; never registered
	RateLimits    map[string]RateLimit // From DROIDMCP_<SERVER>_RATE_LIMITS, by tool name or AllTools
}

// AllTools is the DROIDMCP_<SERVER>_RATE_LIMITS key for a limit on every call
// to the server, whichever tool it names.
const AllTools = "*"

// maxRateLimitCalls bounds N in "N/<unit>": the limiter keeps one timestamp per
// allowed call in the window.
const maxRateLimitCalls = 100_000

// RateLimit allows at most Calls tool calls in any rolling window of length
// Per. The zero value means no limit: it is how "off" switches a built-in
// default off.
type RateLimit struct {
	Calls int
	Per   time.Duration
}

// Off reports whether the limit is the "no limit" zero value.
func (l RateLimit) Off() bool {
	return l.Calls == 0
}

// rateUnits maps the unit suffixes accepted in "N/<unit>" to their durations.
var rateUnits = map[string]time.Duration{
	"s": time.Second,
	"m": time.Minute,
	"h": time.Hour,
	"d": 24 * time.Hour,
}

// String renders the limit in the "N/<unit>" form it is configured with.
func (l RateLimit) String() string {
	if l.Off() {
		return "off"
	}
	for unit, d := range rateUnits {
		if l.Per == d {
			return fmt.Sprintf("%d/%s", l.Calls, unit)
		}
	}
	return fmt.Sprintf("%d/%s", l.Calls, l.Per)
}

// ParseRateLimit reads one limit: "N/s", "N/m", "N/h", "N/d" (N calls per
// second, minute, hour or day), or "off".
func ParseRateLimit(spec string) (RateLimit, error) {
	spec = strings.TrimSpace(spec)
	if strings.EqualFold(spec, "off") {
		return RateLimit{}, nil
	}
	count, unit, ok := strings.Cut(spec, "/")
	per, known := rateUnits[strings.TrimSpace(unit)]
	if !ok || !known {
		return RateLimit{}, fmt.Errorf("rate limit %q: want N/s, N/m, N/h, N/d or off", spec)
	}
	n, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil || n < 1 || n > maxRateLimitCalls {
		return RateLimit{}, fmt.Errorf("rate limit %q: the count must be a whole number from 1 to %d", spec, maxRateLimitCalls)
	}
	return RateLimit{Calls: n, Per: per}, nil
}

// parseRateLimits reads a DROIDMCP_<SERVER>_RATE_LIMITS value: comma-separated
// "tool=limit" entries, where tool is a tool name or AllTools. Whether each
// name is a real tool is checked later, against the registered tools.
func parseRateLimits(raw string) (map[string]RateLimit, error) {
	out := map[string]RateLimit{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, spec, ok := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("%q: want tool=limit, e.g. send_sms=10/h or *=120/m", entry)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("%q is listed twice", name)
		}
		limit, err := ParseRateLimit(spec)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = limit
	}
	return out, nil
}

// LoadConfig reads the shared settings for the named server (the cmd/<name>
// directory name): DROIDMCP_PORT, DROIDMCP_ROOT, DROIDMCP_TRANSPORT,
// DROIDMCP_<SERVER>_DISABLED_TOOLS and DROIDMCP_<SERVER>_RATE_LIMITS. It
// applies defaults — including the server's own default port — and validates
// them. Viper is used locally to
// source the env values and then discarded: no configuration state is retained
// beyond the returned Config, and per-process isolation keeps tests independent.
func LoadConfig(server string) (*Config, error) {
	port, ok := defaultPorts[server]
	if !ok {
		return nil, fmt.Errorf("config: unknown server %q", server)
	}

	v := viper.New()
	v.SetDefault("PORT", port)
	v.SetDefault("ROOT", defaultRoot)
	v.SetDefault("TRANSPORT", TransportHTTP)

	v.SetEnvPrefix(envPrefix)
	// Replace dots with underscores in env keys to support nested structs if needed.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	limits, err := parseRateLimits(os.Getenv(RateLimitsVar(server)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", RateLimitsVar(server), err)
	}

	cfg := &Config{
		Server:        server,
		Port:          v.GetInt("PORT"),
		Root:          v.GetString("ROOT"),
		Transport:     strings.ToLower(strings.TrimSpace(v.GetString("TRANSPORT"))),
		DisabledTools: parseList(os.Getenv(DisabledToolsVar(server))),
		RateLimits:    limits,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate enforces invariants the rest of the code depends on. It runs
// once at LoadConfig time so per-server main() functions can fail-fast
// with a clear message instead of crashing later on a bad port or a
// missing root directory.
func (c *Config) validate() error {
	if c.Port < minPort || c.Port > maxPort {
		return fmt.Errorf("DROIDMCP_PORT out of range: got %d, want [%d, %d]", c.Port, minPort, maxPort)
	}
	if c.Transport != TransportHTTP && c.Transport != TransportStdio {
		return fmt.Errorf("DROIDMCP_TRANSPORT must be %q or %q, got %q", TransportHTTP, TransportStdio, c.Transport)
	}
	if c.Root == "" {
		return errors.New("DROIDMCP_ROOT must not be empty")
	}
	info, err := os.Stat(c.Root)
	if err != nil {
		return fmt.Errorf("DROIDMCP_ROOT %q: %w", c.Root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("DROIDMCP_ROOT %q: not a directory", c.Root)
	}
	return nil
}

// Stdio reports whether the server talks MCP over stdin/stdout instead of an
// HTTP listener. With stdio only the launching process can reach the server,
// so the servers that otherwise refuse to start without an API key accept
// running without one.
func (c *Config) Stdio() bool {
	return c.Transport == TransportStdio
}

// ToolDisabled reports whether the operator listed the tool in
// DROIDMCP_<SERVER>_DISABLED_TOOLS.
func (c *Config) ToolDisabled(name string) bool {
	for _, t := range c.DisabledTools {
		if t == name {
			return true
		}
	}
	return false
}

// DisabledToolsVar names the variable that lists a server's disabled tools,
// e.g. DROIDMCP_TERMUX_DISABLED_TOOLS.
func DisabledToolsVar(server string) string {
	return envPrefix + "_" + strings.ToUpper(server) + "_DISABLED_TOOLS"
}

// RateLimitsVar names the variable that sets a server's rate limits, e.g.
// DROIDMCP_SMS_RATE_LIMITS.
func RateLimitsVar(server string) string {
	return envPrefix + "_" + strings.ToUpper(server) + "_RATE_LIMITS"
}

// parseList splits a comma-separated value, trimming entries and dropping
// empty ones and repeats.
func parseList(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// ResolveAPIKey returns the API key the named server should enforce on inbound
// requests. It checks the per-server variable DROIDMCP_<NAME>_KEY first, then
// falls back to the global DROIDMCP_API_KEY. An empty result means no auth is
// configured (dev mode); callers that require a key must enforce that themselves.
func ResolveAPIKey(serverName string) string {
	specific := envPrefix + "_" + strings.ToUpper(serverName) + "_KEY"
	if k := os.Getenv(specific); k != "" {
		return k
	}
	return os.Getenv(envPrefix + "_API_KEY")
}
