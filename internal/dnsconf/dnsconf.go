// Package dnsconf makes DNS work in the cross-compiled release binaries on
// Android.
//
// A CGO_ENABLED=0 binary resolves names with Go's own resolver, which reads
// /etc/resolv.conf. Android has none, so that resolver falls back to
// localhost:53, where nothing listens, and every lookup fails. Termux keeps
// its nameservers in $PREFIX/etc/resolv.conf instead; Termux's own Go is
// patched to read it, but the release binaries are built with upstream Go.
package dnsconf

import (
	"bufio"
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// SystemPath is the resolv.conf Go's resolver reads.
const SystemPath = "/etc/resolv.conf"

// termuxPrefix is Termux's $PREFIX, for a server started without it in its
// environment.
const termuxPrefix = "/data/data/com.termux/files/usr"

// Path returns the resolv.conf that names this device's DNS servers:
// /etc/resolv.conf, or Termux's when the system has none. It returns "" when
// neither exists.
func Path() string {
	for _, p := range []string{SystemPath, termuxPath()} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func termuxPath() string {
	prefix := os.Getenv("PREFIX")
	if prefix == "" {
		prefix = termuxPrefix
	}
	return filepath.Join(prefix, "etc", "resolv.conf")
}

// Nameservers returns the addresses on the nameserver lines of the
// resolv.conf at path, or nil if it cannot be read.
func Nameservers(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return out
}

// Install points net.DefaultResolver at Termux's nameservers when the system
// has no /etc/resolv.conf, and returns them as host:port; elsewhere it changes
// nothing and returns nil. Every lookup through the default resolver —
// net.LookupIP, the dialers of the HTTP clients — then works on Android.
//
// It sets only Resolver.Dial, not PreferGo: a binary built with cgo (Termux's
// own Go builds that way) keeps resolving through Android's libc, which
// follows the network's DNS, Private DNS and VPNs; only a binary without cgo,
// which has no resolver but Go's, uses Termux's fixed servers.
func Install() []string {
	return install(net.DefaultResolver, SystemPath, termuxPath())
}

func install(r *net.Resolver, system, termux string) []string {
	if _, err := os.Stat(system); err == nil {
		return nil
	}
	var servers []string
	for _, ns := range Nameservers(termux) {
		if addr, err := netip.ParseAddr(ns); err == nil {
			servers = append(servers, netip.AddrPortFrom(addr, 53).String())
		}
	}
	if len(servers) == 0 {
		return nil
	}
	r.Dial = dialer(servers)
	return servers
}

// dialer returns a Resolver.Dial that sends each query to the next of servers
// in turn, ignoring the localhost address Go's resolver asks for. Taking turns
// is the failover: the resolver retries a query that times out, and the retry
// goes to another server.
func dialer(servers []string) func(context.Context, string, string) (net.Conn, error) {
	var next atomic.Uint32
	n := uint32(len(servers))
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		server := servers[(next.Add(1)-1)%n]
		var d net.Dialer
		return d.DialContext(ctx, network, server)
	}
}
