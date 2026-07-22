package main

import (
	"net"
	"strings"
	"testing"
)

func TestValidateURLBlocksScheme(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"gopher://example.com/",
		"ftp://example.com/",
		"javascript:alert(1)",
	}
	for _, raw := range cases {
		if err := validateURL(raw); err == nil {
			t.Fatalf("expected scheme block for %q", raw)
		}
	}
}

func TestValidateURLBlocksPrivateLiteralIPs(t *testing.T) {
	t.Setenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE", "")
	cases := []string{
		"http://127.0.0.1/",
		"http://localhost/", // localhost resolves to loopback
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/", // EC2 metadata classic
		"http://[::1]/",
		"http://100.64.0.1/", // CGNAT
	}
	for _, raw := range cases {
		err := validateURL(raw)
		if err == nil {
			t.Fatalf("expected block for %q", raw)
		}
	}
}

func TestValidateURLAllowsPublicLiteralIP(t *testing.T) {
	t.Setenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE", "")
	if err := validateURL("https://1.1.1.1/"); err != nil {
		t.Fatalf("expected 1.1.1.1 to pass, got %v", err)
	}
}

func TestValidateURLAllowPrivateOptIn(t *testing.T) {
	t.Setenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE", "1")
	if err := validateURL("http://127.0.0.1/"); err != nil {
		t.Fatalf("expected loopback to be allowed when opted in, got %v", err)
	}
}

func TestValidateURLRejectsMissingHost(t *testing.T) {
	if err := validateURL("http:///path"); err == nil {
		t.Fatal("expected missing-host error")
	}
}

func TestIsPublicIP(t *testing.T) {
	publics := []string{"1.1.1.1", "8.8.8.8", "203.0.113.1"}
	privates := []string{
		"127.0.0.1", "::1", "0.0.0.0", "10.1.2.3", "172.16.0.1",
		"192.168.1.1", "169.254.169.254", "224.0.0.1", "100.64.0.1",
		"fc00::1", // IPv6 ULA
		"fe80::1", // IPv6 link-local
	}
	for _, ipStr := range publics {
		if !isPublicIP(net.ParseIP(ipStr)) {
			t.Errorf("expected %s to be public", ipStr)
		}
	}
	for _, ipStr := range privates {
		if isPublicIP(net.ParseIP(ipStr)) {
			t.Errorf("expected %s to be non-public", ipStr)
		}
	}
}

// errBlockedHost is an error type, not a string match — assert that the
// formatter at least mentions the resolved address so logs are useful.
func TestValidateHostMessage(t *testing.T) {
	t.Setenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE", "")
	err := validateHost("127.0.0.1")
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("expected error mentioning the address, got %v", err)
	}
}

func TestSafeDialControlBlocksPrivateResolvedIP(t *testing.T) {
	t.Setenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE", "")
	// safeDialControl sees the concrete post-resolution address, so a name that
	// rebinds to a private IP after passing validateURL is still refused here.
	for _, addr := range []string{"10.0.0.5:80", "127.0.0.1:443", "169.254.1.1:80", "100.64.0.1:80", "[::1]:80"} {
		if err := safeDialControl("tcp", addr, nil); err == nil {
			t.Errorf("expected %q to be blocked at dial control", addr)
		}
	}
}

func TestSafeDialControlAllowsPublicIP(t *testing.T) {
	t.Setenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE", "")
	for _, addr := range []string{"8.8.8.8:443", "1.1.1.1:80"} {
		if err := safeDialControl("tcp", addr, nil); err != nil {
			t.Errorf("public %q should be allowed, got %v", addr, err)
		}
	}
}

func TestSafeDialControlAllowPrivateOptIn(t *testing.T) {
	t.Setenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE", "1")
	if err := safeDialControl("tcp", "10.0.0.5:80", nil); err != nil {
		t.Errorf("opt-in should permit private dial, got %v", err)
	}
}

func TestSafeDialControlRejectsNonIP(t *testing.T) {
	t.Setenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE", "")
	if err := safeDialControl("tcp", "not-an-address", nil); err == nil {
		t.Error("a malformed dial address should error")
	}
}
