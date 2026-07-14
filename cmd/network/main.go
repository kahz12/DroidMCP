// Command network provides an MCP server for local network discovery.
// Every tool returns JSON. Targets are restricted to private ranges (RFC1918
// + link-local + loopback + IPv6 ULA) by default; set
// DROIDMCP_NETWORK_ALLOW_PUBLIC=1 to opt in to public targets (audit 2.10).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kahz12/droidmcp/internal/buildinfo"
	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
)

var cfg *config.Config

// devices is the persistent known-host store shared by scan_network (writer)
// and list_devices / get_device_info (readers). nil until main() sets it, so
// handlers must nil-check; unit tests inject their own temp-backed store.
var devices *inventory

func main() {
	var err error
	cfg, err = config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	devices = newInventory(inventoryPath())

	server := core.NewDroidServer("mcp-network", buildinfo.Version)
	server.APIKey = config.ResolveAPIKey("network")
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	add := func(t mcp.Tool, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
		s.MCPServer.AddTool(t, h)
	}

	add(mcp.NewTool("scan_network",
		mcp.WithDescription("Scan a private subnet for active hosts. Returns JSON with IP, MAC (from ARP) and open ports."),
		mcp.WithString("subnet", mcp.Description("CIDR to scan (e.g. 192.168.1.0/24). If empty, the local subnet is auto-detected.")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 30s, max 120s.")),
	), handleScanNetwork)

	add(mcp.NewTool("check_ports",
		mcp.WithDescription("Concurrent TCP port check on a single host. Returns JSON {host, resolved, ports: [{port, open}]}."),
		mcp.WithString("host", mcp.Required(), mcp.Description("Host to check (IP or hostname)")),
		mcp.WithString("ports", mcp.Description("Comma-separated list of ports. Default: common ports.")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 60s.")),
	), handleCheckPorts)

	add(mcp.NewTool("nslookup",
		mcp.WithDescription("Forward DNS lookup. Returns JSON {host, addrs}."),
		mcp.WithString("host", mcp.Required(), mcp.Description("Hostname to resolve")),
	), handleNSLookup)

	add(mcp.NewTool("reverse_dns",
		mcp.WithDescription("Reverse DNS lookup. Returns JSON {ip, names}."),
		mcp.WithString("ip", mcp.Required(), mcp.Description("IP address to look up")),
	), handleReverseDNS)

	add(mcp.NewTool("traceroute",
		mcp.WithDescription("Trace the path to a host. Shells out to traceroute or tracepath; root not required for the latter."),
		mcp.WithString("host", mcp.Required(), mcp.Description("Target host")),
		mcp.WithNumber("max_hops", mcp.Description("Max TTL hops to probe. Default 30.")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 30s, max 120s.")),
	), handleTraceroute)

	add(mcp.NewTool("network_info",
		mcp.WithDescription("Local network metadata: default gateway, DNS servers, interfaces, detected subnet."),
	), handleNetworkInfo)

	add(mcp.NewTool("list_devices",
		mcp.WithDescription("List all devices remembered from previous scan_network runs (persistent inventory). Run scan_network first to populate it."),
	), handleListDevices)

	add(mcp.NewTool("get_device_info",
		mcp.WithDescription("Return the remembered details (MAC, open ports, first/last seen) for a single device by IP or MAC address."),
		mcp.WithString("device", mcp.Required(), mcp.Description("IP address or MAC address of a device seen in a previous scan")),
	), handleGetDeviceInfo)
}

// scanResult is the wire shape for scan_network.
type scanResult struct {
	Subnet string        `json:"subnet"`
	Count  int           `json:"count"`
	Hosts  []scannedHost `json:"hosts"`
	Capped bool          `json:"capped,omitempty"`
	Note   string        `json:"note,omitempty"`
}

func handleScanNetwork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	subnet := strings.TrimSpace(req.GetString("subnet", ""))
	if subnet == "" {
		subnet = getLocalSubnet()
	}
	if subnet == "" {
		return mcp.NewToolResultError("could not detect local subnet and none provided"), nil
	}
	ipnet, err := validateCIDR(subnet)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	timeout := durationFromReq(req, 30*time.Second, 120*time.Second)
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	hosts, scanErr := scanSubnet(cctx, ipnet)
	// Feed the persistent inventory even on a partial (capped/cancelled) scan:
	// whatever we found is still worth remembering. nil store = recording off.
	if devices != nil {
		devices.record(hosts)
	}
	res := scanResult{
		Subnet: ipnet.String(),
		Count:  len(hosts),
		Hosts:  hosts,
	}
	if scanErr != nil {
		// Cap-warning is informational; ctx errors are fatal.
		if errors.Is(scanErr, context.DeadlineExceeded) || errors.Is(scanErr, context.Canceled) {
			res.Note = scanErr.Error()
		} else {
			res.Capped = true
			res.Note = scanErr.Error()
		}
	}
	return jsonResult(res)
}

// portCheck is the per-port entry in checkPortsResult.
type portCheck struct {
	Port int  `json:"port"`
	Open bool `json:"open"`
}

type checkPortsResult struct {
	Host     string      `json:"host"`
	Resolved []string    `json:"resolved,omitempty"`
	Ports    []portCheck `json:"ports"`
}

const defaultCheckPorts = "21,22,23,25,53,80,110,135,139,143,443,445,993,995,1723,3306,3389,5900,8080"

func handleCheckPorts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	host, err := req.RequireString("host")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resolved, err := validateTarget(host)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Dial the address we actually validated instead of re-resolving the
	// hostname at dial time. Re-resolution would reopen a DNS-rebinding hole:
	// a name that validated as private could rebind to a public IP before the
	// TCP dial. validateTarget guarantees resolved is non-empty on success.
	dialHost := resolved[0].String()

	portsRaw := req.GetString("ports", defaultCheckPorts)
	ports, err := parsePorts(portsRaw)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	timeout := durationFromReq(req, 15*time.Second, 60*time.Second)
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type portResult struct {
		Port int
		Open bool
	}
	results := make([]portResult, len(ports))
	var wg sync.WaitGroup
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	sem := make(chan struct{}, 64)
	for i, p := range ports {
		wg.Add(1)
		sem <- struct{}{}
		go func(i, p int) {
			defer wg.Done()
			defer func() { <-sem }()
			conn, err := dialer.DialContext(cctx, "tcp", net.JoinHostPort(dialHost, strconv.Itoa(p)))
			results[i] = portResult{Port: p, Open: err == nil}
			if conn != nil {
				conn.Close()
			}
		}(i, p)
	}
	wg.Wait()

	out := checkPortsResult{Host: host, Ports: make([]portCheck, len(results))}
	for _, ip := range resolved {
		out.Resolved = append(out.Resolved, ip.String())
	}
	for i, r := range results {
		out.Ports[i] = portCheck(r)
	}
	return jsonResult(out)
}

type nslookupResult struct {
	Host  string   `json:"host"`
	Addrs []string `json:"addrs"`
}

func handleNSLookup(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	host, err := req.RequireString("host")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resolver := &net.Resolver{}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve %s: %v", host, err)), nil
	}
	out := nslookupResult{Host: host}
	for _, ip := range ips {
		out.Addrs = append(out.Addrs, ip.IP.String())
	}
	sort.Strings(out.Addrs)
	return jsonResult(out)
}

type reverseDNSResult struct {
	IP    string   `json:"ip"`
	Names []string `json:"names"`
}

func handleReverseDNS(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ipStr, err := req.RequireString("ip")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid IP %q", ipStr)), nil
	}
	resolver := &net.Resolver{}
	names, err := resolver.LookupAddr(ctx, ip.String())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reverse %s: %v", ip, err)), nil
	}
	for i, n := range names {
		names[i] = strings.TrimSuffix(n, ".")
	}
	sort.Strings(names)
	return jsonResult(reverseDNSResult{IP: ip.String(), Names: names})
}

type tracerouteResult struct {
	Host string `json:"host"`
	Tool string `json:"tool"`
	Raw  string `json:"raw"`
}

// handleTraceroute shells out to the system `traceroute` (or `tracepath`)
// because pure-Go TCP/UDP traceroute requires CAP_NET_RAW to read the ICMP
// time-exceeded replies. tracepath does not need root and is usually the
// best fit on Termux + Android.
func handleTraceroute(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	host, err := req.RequireString("host")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resolved, err := validateTarget(host)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	maxHops := req.GetInt("max_hops", 30)
	if maxHops <= 0 || maxHops > 64 {
		maxHops = 30
	}
	timeout := durationFromReq(req, 30*time.Second, 120*time.Second)
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Trace the validated IP rather than letting the external tool re-resolve
	// the hostname, which would bypass the private-target policy via DNS
	// rebinding. validateTarget guarantees resolved is non-empty on success.
	tool, args, err := chooseTracerouteTool(resolved[0].String(), maxHops)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	cmd := exec.CommandContext(cctx, tool, args...)
	out, runErr := cmd.CombinedOutput()
	res := tracerouteResult{Host: host, Tool: tool, Raw: string(out)}
	if runErr != nil {
		// Surface stderr (already in CombinedOutput) plus the wrapper error.
		res.Raw = strings.TrimSpace(res.Raw)
		if res.Raw != "" {
			res.Raw += "\n"
		}
		res.Raw += runErr.Error()
		body, _ := json.Marshal(res)
		return mcp.NewToolResultError(string(body)), nil
	}
	return jsonResult(res)
}

// chooseTracerouteTool prefers `tracepath` (no root) on Termux/Android,
// falling back to `traceroute -n` otherwise. The audit asks for "no root";
// we never invoke ICMP traceroute.
func chooseTracerouteTool(host string, maxHops int) (string, []string, error) {
	if path, err := exec.LookPath("tracepath"); err == nil {
		return path, []string{"-n", "-m", strconv.Itoa(maxHops), host}, nil
	}
	if path, err := exec.LookPath("traceroute"); err == nil {
		// -T attempts TCP traceroute; on hosts where it requires root the
		// binary usually falls back to UDP without prompting. -n suppresses
		// reverse DNS for speed.
		return path, []string{"-n", "-m", strconv.Itoa(maxHops), host}, nil
	}
	return "", nil, errors.New("no tracepath or traceroute binary on PATH; install inetutils to enable this tool")
}

func handleNetworkInfo(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return jsonResult(gatherNetworkInfo())
}

type listDevicesResult struct {
	Count   int            `json:"count"`
	Devices []deviceRecord `json:"devices"`
	Note    string         `json:"note,omitempty"`
}

func handleListDevices(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if devices == nil {
		return jsonResult(listDevicesResult{Devices: []deviceRecord{}, Note: "device inventory unavailable"})
	}
	list := devices.list()
	return jsonResult(listDevicesResult{Count: len(list), Devices: list})
}

func handleGetDeviceInfo(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("device")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if devices == nil {
		return mcp.NewToolResultError("device inventory unavailable"), nil
	}
	rec, ok := devices.get(id)
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("no known device matching %q; run scan_network first", id)), nil
	}
	return jsonResult(rec)
}

// parsePorts turns a comma-separated list of port numbers into a sorted
// dedup'd []int. Rejects values outside 1..65535.
func parsePorts(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	seen := make(map[int]struct{}, len(parts))
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid port %q", p)
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("no ports specified")
	}
	sort.Ints(out)
	return out, nil
}

// durationFromReq pulls timeout_seconds from the request and clamps to the
// allowed range, defaulting to def when absent or non-positive.
func durationFromReq(req mcp.CallToolRequest, def, max time.Duration) time.Duration {
	t := req.GetInt("timeout_seconds", 0)
	if t <= 0 {
		return def
	}
	d := time.Duration(t) * time.Second
	if d > max {
		return max
	}
	return d
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}
