package dnsconf

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNameservers(t *testing.T) {
	path := writeFile(t, "# Termux\nsearch lan\nnameserver 8.8.8.8\n;nameserver 9.9.9.9\nnameserver\n  nameserver 2001:4860:4860::8888  \n")
	want := []string{"8.8.8.8", "2001:4860:4860::8888"}
	if got := Nameservers(path); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := Nameservers(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Errorf("missing file: got %q", got)
	}
}

func TestInstall(t *testing.T) {
	termux := writeFile(t, "nameserver 8.8.8.8\nnameserver not-an-ip\nnameserver 2001:4860:4860::8888\n")
	missing := filepath.Join(t.TempDir(), "missing")

	t.Run("system resolv.conf wins", func(t *testing.T) {
		r := &net.Resolver{}
		if got := install(r, writeFile(t, "nameserver 10.0.0.1\n"), termux); got != nil || r.Dial != nil {
			t.Fatalf("changed the resolver although /etc/resolv.conf exists: %q", got)
		}
	})
	t.Run("Termux servers when the system has none", func(t *testing.T) {
		r := &net.Resolver{}
		got := install(r, missing, termux)
		if want := []string{"8.8.8.8:53", "[2001:4860:4860::8888]:53"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
		if r.Dial == nil || r.PreferGo {
			t.Fatalf("want Dial set and PreferGo left alone: %+v", r)
		}
	})
	t.Run("nothing to use", func(t *testing.T) {
		for _, termux := range []string{missing, writeFile(t, "# empty\n")} {
			r := &net.Resolver{}
			if got := install(r, missing, termux); got != nil || r.Dial != nil {
				t.Fatalf("%s: got %q", termux, got)
			}
		}
	})
}

func TestDialerTakesTurns(t *testing.T) {
	a, b := fakeDNS(t, [4]byte{192, 0, 2, 1}), fakeDNS(t, [4]byte{192, 0, 2, 2})
	dial := dialer([]string{a, b})
	for i, want := range []string{a, b, a} {
		conn, err := dial(context.Background(), "udp", "127.0.0.1:53")
		if err != nil {
			t.Fatal(err)
		}
		if got := conn.RemoteAddr().String(); got != want {
			t.Errorf("query %d went to %s, want %s", i+1, got, want)
		}
		conn.Close()
	}
}

// Go's resolver must actually use Dial for the address it computed itself
// (localhost:53, with no resolv.conf).
func TestDialerResolves(t *testing.T) {
	r := &net.Resolver{PreferGo: true, Dial: dialer([]string{fakeDNS(t, [4]byte{192, 0, 2, 7})})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs, err := r.LookupHost(ctx, "droidmcp.test.")
	if err != nil || len(addrs) != 1 || addrs[0] != "192.0.2.7" {
		t.Fatalf("got %q, %v", addrs, err)
	}
}

// fakeDNS answers every A query with ip and every other query with no
// records, on a UDP socket on 127.0.0.1, and returns its address.
func fakeDNS(t *testing.T, ip [4]byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			// The question starts after the 12-byte header: a name of
			// length-prefixed labels ending in 0, then type and class.
			end := 12
			for end < n && buf[end] != 0 {
				end += int(buf[end]) + 1
			}
			end += 5
			if end > n {
				continue
			}
			isA := buf[end-4] == 0 && buf[end-3] == 1
			var answers byte
			if isA {
				answers = 1
			}
			resp := append([]byte{buf[0], buf[1], 0x81, 0x80, 0, 1, 0, answers, 0, 0, 0, 0}, buf[12:end]...)
			if isA {
				// Name: pointer to the question's; type A, class IN, TTL 60.
				resp = append(resp, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, ip[0], ip[1], ip[2], ip[3])
			}
			_, _ = pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().String()
}
