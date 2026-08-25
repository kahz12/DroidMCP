// Ollama endpoint resolution and the HTTP client every tool goes through.
//
// The threat model here is the mirror image of the scraper's. The scraper takes
// a URL from the caller and must keep it away from the local network; this
// server always talks to a local inference daemon, so the destination is fixed
// by the operator (DROIDMCP_OLLAMA_HOST) and is never influenced by tool
// arguments. What we guard against instead is the host pointing off-device,
// which would ship every prompt to a third party: that requires an explicit
// opt-in via DROIDMCP_LLMPROXY_ALLOW_REMOTE.
package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const (
	// defaultOllamaHost pins the IPv4 loopback literal on purpose. Under Termux
	// and proot "localhost" often resolves to ::1 only, and Ollama binds v4 by
	// default, so a "localhost" default fails with an opaque dial error on the
	// very setup this project targets.
	defaultOllamaHost = "http://127.0.0.1:11434"
	defaultOllamaPort = "11434"
)

var (
	errRemoteHost = errors.New("ollama host is not local")
	errBadScheme  = errors.New("ollama host scheme must be http or https")
)

// parseBaseURL turns the raw DROIDMCP_OLLAMA_HOST value into a normalized base
// URL. It accepts a bare "host", a "host:port" pair or a full URL, fills in the
// default Ollama port, and strips any path so callers can append "/api/..."
// without worrying about double slashes. An empty value yields the loopback
// default.
func parseBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultOllamaHost
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid ollama host %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: got %q", errBadScheme, u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("invalid ollama host %q: missing host", raw)
	}

	host := u.Host
	if port := u.Port(); port == "" {
		host = net.JoinHostPort(u.Hostname(), defaultOllamaPort)
	} else {
		// url.Parse only checks that the port is digits. An out-of-range one
		// would pass every startup check and then fail at dial time with an
		// error blaming the daemon, so reject it here where the message can
		// still name the real problem.
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid ollama host %q: port out of range [1, 65535]", raw)
		}
	}

	// Rebuild from the parts we accept so a stray path, query or fragment in the
	// env value cannot leak into every request path we later join onto it.
	return &url.URL{Scheme: u.Scheme, Host: host}, nil
}

// validateBase refuses a base URL that leaves the device unless the operator
// opted in. Loopback, RFC1918, link-local and CGNAT addresses all count as
// local: running Ollama on a desktop on the same LAN is a legitimate setup for
// a phone, while a public address means prompts leave the network.
func validateBase(base *url.URL, allowRemote bool) error {
	if allowRemote {
		return nil
	}
	host := base.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}

	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("cannot resolve ollama host %q: %w", host, err)
		}
		ips = resolved
	}

	for _, ip := range ips {
		if !isLocalIP(ip) {
			return fmt.Errorf("%w: %s resolves to %s; set DROIDMCP_LLMPROXY_ALLOW_REMOTE=1 to send prompts off-device",
				errRemoteHost, host, ip)
		}
	}
	return nil
}

// isLocalIP reports whether ip belongs to a range that stays on the device or
// on the local network.
func isLocalIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	// 100.64.0.0/10 (CGNAT) is what tethered and some carrier-side networks
	// hand out; Go's IsPrivate does not cover it.
	cgn := &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	return cgn.Contains(ip)
}
