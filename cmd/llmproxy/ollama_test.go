package main

import (
	"errors"
	"testing"
)

func TestParseBaseURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty falls back to the loopback default", "", "http://127.0.0.1:11434"},
		{"blank falls back to the loopback default", "   ", "http://127.0.0.1:11434"},
		{"bare host:port gets an http scheme", "127.0.0.1:11434", "http://127.0.0.1:11434"},
		{"bare host without port gets the ollama port", "127.0.0.1", "http://127.0.0.1:11434"},
		{"full url is kept as is", "http://127.0.0.1:11434", "http://127.0.0.1:11434"},
		{"trailing slash is trimmed", "http://127.0.0.1:11434/", "http://127.0.0.1:11434"},
		{"https is preserved", "https://box.lan:11434", "https://box.lan:11434"},
		{"ipv6 literal keeps its brackets", "[::1]:11434", "http://[::1]:11434"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBaseURL(tc.raw)
			if err != nil {
				t.Fatalf("parseBaseURL(%q) returned error: %v", tc.raw, err)
			}
			if got.String() != tc.want {
				t.Errorf("parseBaseURL(%q) = %q, want %q", tc.raw, got.String(), tc.want)
			}
		})
	}
}

func TestParseBaseURLRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"non-http scheme", "ftp://127.0.0.1:11434"},
		{"file scheme", "file:///etc/passwd"},
		{"scheme without host", "http://"},
		{"unparseable", "http://[::1"},
		// url.Parse only checks that the port is digits, so an out-of-range one
		// would start the server and then fail every call at dial time with a
		// message blaming the daemon.
		{"port above the range", "127.0.0.1:99999"},
		{"port zero", "127.0.0.1:0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBaseURL(tc.raw)
			if err == nil {
				t.Fatalf("parseBaseURL(%q) = %q, want an error", tc.raw, got.String())
			}
		})
	}
}

// localhost resolves to ::1 only inside some Termux/proot setups, which makes a
// "localhost" default fail with a confusing dial error. The default is pinned to
// the IPv4 literal so the out-of-the-box config reaches ollama serve.
func TestDefaultBaseURLIsIPv4Loopback(t *testing.T) {
	got, err := parseBaseURL("")
	if err != nil {
		t.Fatalf("parseBaseURL(\"\") returned error: %v", err)
	}
	if got.Hostname() != "127.0.0.1" {
		t.Errorf("default host = %q, want 127.0.0.1", got.Hostname())
	}
}

func TestValidateBaseBlocksRemoteHostsUnlessOptedIn(t *testing.T) {
	remote, err := parseBaseURL("http://198.51.100.7:11434")
	if err != nil {
		t.Fatalf("parseBaseURL returned error: %v", err)
	}

	if err := validateBase(remote, false); !errors.Is(err, errRemoteHost) {
		t.Errorf("validateBase(public, allowRemote=false) = %v, want errRemoteHost", err)
	}
	if err := validateBase(remote, true); err != nil {
		t.Errorf("validateBase(public, allowRemote=true) = %v, want nil", err)
	}
}

func TestValidateBaseAllowsLocalHosts(t *testing.T) {
	cases := []string{
		"http://127.0.0.1:11434",
		"http://[::1]:11434",
		"http://192.168.1.40:11434",
		"http://10.0.0.5:11434",
	}

	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			base, err := parseBaseURL(raw)
			if err != nil {
				t.Fatalf("parseBaseURL(%q) returned error: %v", raw, err)
			}
			if err := validateBase(base, false); err != nil {
				t.Errorf("validateBase(%q, allowRemote=false) = %v, want nil", raw, err)
			}
		})
	}
}
