// Network-target policy for the scanner. By default we refuse anything
// that is not RFC1918 / link-local / loopback / IPv6 ULA, because scanning
// the open internet from a user's phone is at best impolite and in some
// jurisdictions illegal. Operators that genuinely need to scan a public
// target opt in via DROIDMCP_NETWORK_ALLOW_PUBLIC=1.
package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

var errPublicTarget = errors.New("target is not in a private network range")

// allowPublicNetworks reports whether DROIDMCP_NETWORK_ALLOW_PUBLIC has been
// set. Off by default; when off, every scan/check_ports target must resolve
// inside a private range.
func allowPublicNetworks() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DROIDMCP_NETWORK_ALLOW_PUBLIC")))
	return v == "1" || v == "true" || v == "yes"
}

// isPrivateIP reports whether ip is in a range we consider safe to scan
// without explicit opt-in: RFC1918, loopback, link-local (IPv4 169.254/16
// and IPv6 fe80::/10), IPv6 ULA (fc00::/7) and unspecified. CGNAT
// (100.64.0.0/10) is included because while it is reserved it shows up on
// some carrier networks and the user is plausibly authorised to scan it.
func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if cgn := (&net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}); cgn.Contains(ip) {
		return true
	}
	return false
}

// validateTarget rejects host unless every resolved IP is private (or the
// operator has opted in to public scans). The host argument may already be
// an IP literal, in which case no DNS lookup happens.
func validateTarget(host string) ([]net.IP, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("host is empty")
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}
		ips = resolved
	}
	if allowPublicNetworks() {
		return ips, nil
	}
	for _, ip := range ips {
		if !isPrivateIP(ip) {
			return nil, fmt.Errorf("%w: %s -> %s", errPublicTarget, host, ip.String())
		}
	}
	return ips, nil
}

// validateCIDR enforces the same private-network policy on a subnet. The
// returned IPNet is never nil on success.
func validateCIDR(cidr string) (*net.IPNet, error) {
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	if allowPublicNetworks() {
		return ipnet, nil
	}
	// A CIDR is "private" if its network address is private. We don't try
	// to validate every address inside it; the policy is at the network
	// boundary.
	if !isPrivateIP(ipnet.IP) {
		return nil, fmt.Errorf("%w: %s", errPublicTarget, cidr)
	}
	return ipnet, nil
}
