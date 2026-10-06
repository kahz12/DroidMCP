package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
}

func resultText(t *testing.T, res *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected at least one content block")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text, res.IsError
}

func TestHandleScanNetworkRejectsPublic(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	res, err := handleScanNetwork(context.Background(), callRequest(map[string]any{
		"subnet": "1.1.1.0/24",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatalf("expected public CIDR to be rejected, got %s", text)
	}
}

func TestHandleScanNetworkPrivateAcceptsAndReturnsJSON(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	// /30 = 4 addresses, so the scan completes quickly even if no listener
	// answers. We just want JSON to come out, not actual hosts.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listen: %v", err)
	}
	ln.Close()

	res, err := handleScanNetwork(context.Background(), callRequest(map[string]any{
		"subnet":          "127.0.0.0/30",
		"timeout_seconds": float64(3),
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := resultText(t, res)
	var got scanResult
	if jerr := json.Unmarshal([]byte(text), &got); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, text)
	}
	if got.Subnet != "127.0.0.0/30" {
		t.Errorf("subnet: %q", got.Subnet)
	}
	// Count must agree with the actual hosts slice, or the JSON contract lies.
	if got.Count != len(got.Hosts) {
		t.Errorf("count %d disagrees with hosts length %d", got.Count, len(got.Hosts))
	}
}

func TestHandleCheckPortsRejectsPublic(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	res, err := handleCheckPorts(context.Background(), callRequest(map[string]any{
		"host":  "1.1.1.1",
		"ports": "80",
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected public host to be rejected")
	}
}

func TestHandleCheckPortsLocalhostJSON(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	res, err := handleCheckPorts(context.Background(), callRequest(map[string]any{
		"host":  "127.0.0.1",
		"ports": "22," + intStr(port),
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var got checkPortsResult
	if jerr := json.Unmarshal([]byte(text), &got); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, text)
	}
	if got.Host != "127.0.0.1" {
		t.Errorf("host: %q", got.Host)
	}
	openCount := 0
	for _, p := range got.Ports {
		if p.Open {
			openCount++
		}
	}
	if openCount == 0 {
		t.Errorf("expected at least one open port (the listener), got %+v", got.Ports)
	}
}

func TestHandleCheckPortsInvalidList(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	res, err := handleCheckPorts(context.Background(), callRequest(map[string]any{
		"host":  "127.0.0.1",
		"ports": "abc,80",
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected error for malformed ports list")
	}
}

func TestHandleNSLookupLocalhost(t *testing.T) {
	res, err := handleNSLookup(context.Background(), callRequest(map[string]any{
		"host": "localhost",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Skipf("DNS not available in this env: %s", text)
	}
	var got nslookupResult
	if jerr := json.Unmarshal([]byte(text), &got); jerr != nil {
		t.Fatalf("not JSON: %v", jerr)
	}
	if len(got.Addrs) == 0 {
		t.Errorf("expected at least one addr for localhost, got %+v", got)
	}
}

func TestHandleReverseDNSInvalid(t *testing.T) {
	res, err := handleReverseDNS(context.Background(), callRequest(map[string]any{
		"ip": "not-an-ip",
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected error for malformed IP")
	}
}

func TestHandleNetworkInfoReturnsJSON(t *testing.T) {
	dir := t.TempDir()
	// Fake /proc/net/route and resolv.conf so the test is deterministic.
	route := filepath.Join(dir, "route")
	body := "Iface\tDestination\tGateway\tFlags\n" +
		"wlan0\t00000000\t0101A8C0\t0003\n"
	if err := os.WriteFile(route, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	resolv := filepath.Join(dir, "resolv.conf")
	if err := os.WriteFile(resolv, []byte("nameserver 1.2.3.4\nnameserver 5.6.7.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prevR, prevN := procRoutePath, resolvConfPath
	procRoutePath = route
	resolvConfPath = resolv
	t.Cleanup(func() { procRoutePath, resolvConfPath = prevR, prevN })

	res, err := handleNetworkInfo(context.Background(), callRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var got networkInfo
	if jerr := json.Unmarshal([]byte(text), &got); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, text)
	}
	if got.DefaultGateway != "192.168.1.1" {
		t.Errorf("gateway: %q", got.DefaultGateway)
	}
	if !sliceContains(got.DNSServers, "1.2.3.4") || !sliceContains(got.DNSServers, "5.6.7.8") {
		t.Errorf("dns_servers: %+v", got.DNSServers)
	}
}

func TestDefaultGatewayMissingFile(t *testing.T) {
	prev := procRoutePath
	procRoutePath = "/nonexistent/proc/route"
	t.Cleanup(func() { procRoutePath = prev })
	if g := defaultGatewayFromProc(); g != "" {
		t.Errorf("expected empty gateway for missing file, got %q", g)
	}
}

func TestDNSServersMissingFile(t *testing.T) {
	prev := resolvConfPath
	resolvConfPath = "/nonexistent/etc/resolv.conf"
	t.Cleanup(func() { resolvConfPath = prev })
	if d := dnsServersFromResolv(); d != nil {
		t.Errorf("expected nil dns servers, got %+v", d)
	}
}

func TestDurationFromReqClamps(t *testing.T) {
	d := durationFromReq(callRequest(map[string]any{"timeout_seconds": float64(99999)}), 5e9, 60e9)
	if d != 60e9 {
		t.Errorf("expected clamp to 60s, got %v", d)
	}
	d = durationFromReq(callRequest(map[string]any{}), 5e9, 60e9)
	if d != 5e9 {
		t.Errorf("expected default 5s, got %v", d)
	}
}

func TestHandleTracerouteRejectsPublicByDefault(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	res, err := handleTraceroute(context.Background(), callRequest(map[string]any{
		"host": "1.1.1.1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected public target to be rejected")
	}
}

// tracerouteShims replaces PATH with a directory that holds only the named
// tools, so chooseTracerouteTool sees exactly those. Each tool runs script.
func tracerouteShims(t *testing.T, script string, names ...string) string {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("/bin/sh not available: %v", err)
	}
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	return dir
}

const printArgv = "for a in \"$@\"; do printf '%s\\n' \"$a\"; done\n"

func runTraceroute(t *testing.T, args map[string]any) (tracerouteResult, bool) {
	t.Helper()
	res, err := handleTraceroute(context.Background(), callRequest(args))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	var got tracerouteResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	return got, isErr
}

// The external tool must receive the IP validateTarget approved, never the
// hostname: re-resolving it could reach a different (public) address.
func TestTracerouteTracesValidatedIP(t *testing.T) {
	if _, err := net.LookupIP("localhost"); err != nil {
		t.Skipf("localhost does not resolve here: %v", err)
	}
	dir := tracerouteShims(t, printArgv, "tracepath", "traceroute")
	got, isErr := runTraceroute(t, map[string]any{"host": "localhost"})
	if isErr {
		t.Fatalf("unexpected error: %+v", got)
	}
	if got.Tool != filepath.Join(dir, "tracepath") {
		t.Errorf("tool = %q, want tracepath preferred", got.Tool)
	}
	argv := strings.Fields(got.Raw)
	if len(argv) != 4 || argv[0] != "-n" || argv[1] != "-m" || argv[2] != "30" {
		t.Fatalf("argv = %q, want -n -m 30 <ip>", argv)
	}
	if ip := net.ParseIP(argv[3]); ip == nil || !ip.IsLoopback() {
		t.Fatalf("traced %q, want the resolved loopback IP rather than the hostname", argv[3])
	}
}

func TestTracerouteMaxHopsAndFallback(t *testing.T) {
	dir := tracerouteShims(t, printArgv, "traceroute")
	for hops, want := range map[float64]string{5: "5", 64: "64", 0: "30", 65: "30", -1: "30"} {
		got, isErr := runTraceroute(t, map[string]any{"host": "127.0.0.1", "max_hops": hops})
		if isErr {
			t.Fatalf("max_hops=%v: unexpected error: %+v", hops, got)
		}
		if got.Tool != filepath.Join(dir, "traceroute") {
			t.Fatalf("tool = %q, want the traceroute fallback", got.Tool)
		}
		if argv := strings.Fields(got.Raw); strings.Join(argv, " ") != "-n -m "+want+" 127.0.0.1" {
			t.Errorf("max_hops=%v: argv = %q, want -n -m %s 127.0.0.1", hops, argv, want)
		}
	}
}

func TestTracerouteWithoutTools(t *testing.T) {
	tracerouteShims(t, "")
	res, err := handleTraceroute(context.Background(), callRequest(map[string]any{"host": "127.0.0.1"}))
	if err != nil {
		t.Fatal(err)
	}
	if text, isErr := resultText(t, res); !isErr || !strings.Contains(text, "no tracepath or traceroute") {
		t.Fatalf("got %q (error=%v), want the missing-tool error", text, isErr)
	}
}

func TestTracerouteFailureKeepsOutput(t *testing.T) {
	tracerouteShims(t, "echo 'network unreachable' >&2\nexit 2\n", "tracepath")
	got, isErr := runTraceroute(t, map[string]any{"host": "127.0.0.1"})
	if !isErr {
		t.Fatalf("expected an error result, got %+v", got)
	}
	if !strings.Contains(got.Raw, "network unreachable") || !strings.Contains(got.Raw, "exit status 2") {
		t.Fatalf("raw = %q, want the tool output and its exit status", got.Raw)
	}
}

func intStr(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [16]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

func sliceContains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
