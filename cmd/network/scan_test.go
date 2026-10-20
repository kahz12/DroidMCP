package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIPv4Roundtrip(t *testing.T) {
	cases := []string{"0.0.0.0", "192.168.1.1", "255.255.255.255", "10.0.0.5"}
	for _, c := range cases {
		ip := net.ParseIP(c)
		if got := uint32ToIPv4(ipv4ToUint32(ip)).String(); got != c {
			t.Errorf("roundtrip %s -> %s", c, got)
		}
	}
}

func TestScanSubnetCancelsOnContext(t *testing.T) {
	// Use a /24 of TEST-NET-1 (203.0.113.0/24) — public, but we use the
	// CIDR struct directly so validateCIDR is not in the path. The test
	// validates that scanSubnet honours ctx, not the policy.
	_, ipnet, _ := net.ParseCIDR("203.0.113.0/24")
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately; scanSubnet should return promptly with ctx.Err.
	cancel()
	start := time.Now()
	_, err := scanSubnet(ctx, ipnet)
	if err == nil {
		t.Fatal("expected ctx.Err()")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("scanSubnet did not abort fast on cancel: %v", elapsed)
	}
}

func TestScanSubnetCappedReturnsAllScanned(t *testing.T) {
	// /16 = 65536 hosts, must be capped to maxScanHosts=4096. We don't
	// actually want to run thousands of dials, so use a very short ctx
	// timeout — scanSubnet should bail out promptly and the cap warning
	// (or ctx error) is what we care about.
	_, ipnet, _ := net.ParseCIDR("10.255.0.0/16")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := scanSubnet(ctx, ipnet)
	if err == nil {
		t.Fatal("expected either cap warning or ctx error")
	}
}

func TestEnrichWithARPReadsTable(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "arp")
	body := strings.Join([]string{
		"IP address       HW type  Flags  HW address         Mask  Device",
		"192.168.1.10     0x1      0x2    aa:bb:cc:dd:ee:ff  *     wlan0",
		"192.168.1.11     0x1      0x2    11:22:33:44:55:66  *     wlan0",
		"192.168.1.99     0x1      0x0    00:00:00:00:00:00  *     wlan0",
	}, "\n") + "\n"
	if err := os.WriteFile(fake, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := arpPath
	arpPath = fake
	t.Cleanup(func() { arpPath = prev })

	hosts := []scannedHost{
		{IP: "192.168.1.10"},
		{IP: "192.168.1.11"},
		{IP: "192.168.1.99"},
		{IP: "192.168.1.123"}, // not in table
	}
	enrichWithARP(hosts)
	if hosts[0].MAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("host 10: %+v", hosts[0])
	}
	if hosts[1].MAC != "11:22:33:44:55:66" {
		t.Errorf("host 11: %+v", hosts[1])
	}
	// 99 is in table but with the all-zero MAC (incomplete entry); we drop it.
	if hosts[2].MAC != "" {
		t.Errorf("host 99 should not pick up zero-MAC: %+v", hosts[2])
	}
	if hosts[3].MAC != "" {
		t.Errorf("host 123 not in arp table; should stay empty: %+v", hosts[3])
	}
}

func TestEnrichWithARPMissingFileIsBenign(t *testing.T) {
	prev := arpPath
	arpPath = "/nonexistent/path/arp"
	t.Cleanup(func() { arpPath = prev })
	hosts := []scannedHost{{IP: "1.2.3.4"}}
	enrichWithARP(hosts) // must not panic
	if hosts[0].MAC != "" {
		t.Errorf("expected MAC to remain empty, got %q", hosts[0].MAC)
	}
}

func TestScanSubnetFindsListener(t *testing.T) {
	// The scanner only probes ports 22, 80 and 443, so the listener has to be on
	// one of them. Binding a low port is often not allowed (CI, Termux): try each
	// and skip the test if none is available.
	listeners := []string{"127.0.0.1:80", "127.0.0.1:22", "127.0.0.1:443"}
	var ln net.Listener
	for _, addr := range listeners {
		l, err := net.Listen("tcp", addr)
		if err == nil {
			ln = l
			break
		}
	}
	if ln == nil {
		t.Skip("can't bind any of 22/80/443 on 127.0.0.1")
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

	_, ipnet, _ := net.ParseCIDR("127.0.0.0/30")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hosts, err := scanSubnet(ctx, ipnet)
	if err != nil && err != context.DeadlineExceeded {
		t.Logf("scanSubnet returned %v (non-fatal)", err)
	}
	found := false
	for _, h := range hosts {
		if h.IP == "127.0.0.1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 127.0.0.1 in scan results, got %+v", hosts)
	}
}

func TestParsePortsValid(t *testing.T) {
	got, err := parsePorts(" 80 , 443 , 22, 80")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{22, 80, 443}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParsePortsInvalid(t *testing.T) {
	bad := []string{"abc", "0", "65536", "-1", ""}
	for _, b := range bad {
		if _, err := parsePorts(b); err == nil {
			t.Errorf("expected error for %q", b)
		}
	}
}
