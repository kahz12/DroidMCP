package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxScanHosts caps CIDR expansion so a /8 or /16 cannot spawn millions of
// dials on a phone-class device. /20 is already 4096 hosts, which is the
// most it will scan.
const maxScanHosts = 4096

// scanWorkerLimit bounds concurrent dials to keep file-descriptor and
// goroutine pressure manageable.
const scanWorkerLimit = 128

// scannedHost is what the scanner returns per active host. MAC is best-effort
// (read from /proc/net/arp after the scan); empty string when unknown.
type scannedHost struct {
	IP        string   `json:"ip"`
	MAC       string   `json:"mac,omitempty"`
	OpenPorts []string `json:"open_ports,omitempty"`
}

// scanSubnet performs a TCP SYN-style probe of every host in cidr. It honours
// ctx (so the caller can cancel), enforces maxScanHosts, caps concurrency at
// scanWorkerLimit and skips the network/broadcast addresses for masks that
// have them. Returns the active hosts sorted by IP.
func scanSubnet(ctx context.Context, ipnet *net.IPNet) ([]scannedHost, error) {
	ip4 := ipnet.IP.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("only IPv4 CIDRs are supported, got %s", ipnet.String())
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return nil, fmt.Errorf("invalid IPv4 mask: %s", ipnet.String())
	}

	hostBits := bits - ones
	var totalAddresses uint64 = 1 << uint(hostBits)
	capped := false
	if totalAddresses > maxScanHosts {
		totalAddresses = maxScanHosts
		capped = true
	}

	networkInt := ipv4ToUint32(ip4.Mask(ipnet.Mask))
	var startOffset, endOffset uint64 = 0, totalAddresses
	if hostBits >= 2 {
		startOffset = 1
		endOffset = totalAddresses - 1
	}

	sem := make(chan struct{}, scanWorkerLimit)
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make(map[string][]string)

	dialer := &net.Dialer{Timeout: 150 * time.Millisecond}
	portsToTry := []string{"80", "22", "443"}

	for offset := startOffset; offset < endOffset; offset++ {
		select {
		case <-ctx.Done():
			wg.Wait()
			return collectResults(results), ctx.Err()
		default:
		}
		target := uint32ToIPv4(networkInt + uint32(offset)).String()
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()
			for _, p := range portsToTry {
				select {
				case <-ctx.Done():
					return
				default:
				}
				conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target, p))
				if err == nil {
					mu.Lock()
					results[target] = append(results[target], p)
					mu.Unlock()
					conn.Close()
				}
			}
		}(target)
	}
	wg.Wait()

	hosts := collectResults(results)
	enrichWithARP(hosts)
	if capped {
		// Surface the cap to the caller via a sentinel error wrapped with the
		// data; main.go decides whether it's fatal.
		return hosts, fmt.Errorf("subnet too large; capped at %d hosts", maxScanHosts)
	}
	return hosts, nil
}

func collectResults(m map[string][]string) []scannedHost {
	out := make([]scannedHost, 0, len(m))
	for ip, ports := range m {
		sort.Slice(ports, func(i, j int) bool {
			ai, _ := strconv.Atoi(ports[i])
			aj, _ := strconv.Atoi(ports[j])
			return ai < aj
		})
		out = append(out, scannedHost{IP: ip, OpenPorts: ports})
	}
	sort.Slice(out, func(i, j int) bool {
		return ipv4ToUint32(net.ParseIP(out[i].IP)) < ipv4ToUint32(net.ParseIP(out[j].IP))
	})
	return out
}

// arpPath is the kernel-exposed neighbour table. Tests override it.
var arpPath = "/proc/net/arp"

// enrichWithARP fills in the MAC field of every host whose IP appears in
// /proc/net/arp. Best effort: missing file or unparseable entries are silently
// ignored.
func enrichWithARP(hosts []scannedHost) {
	if len(hosts) == 0 {
		return
	}
	table, err := readARPTable()
	if err != nil {
		return
	}
	for i := range hosts {
		if mac, ok := table[hosts[i].IP]; ok && mac != "00:00:00:00:00:00" {
			hosts[i].MAC = mac
		}
	}
}

// readARPTable parses /proc/net/arp into ip -> mac. Header line is skipped.
func readARPTable() (map[string]string, error) {
	f, err := os.Open(arpPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make(map[string]string)
	scanner := bufio.NewScanner(f)
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		ip, mac := fields[0], fields[3]
		if ip == "" || mac == "" {
			continue
		}
		out[ip] = mac
	}
	return out, scanner.Err()
}

func ipv4ToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uint32ToIPv4(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n)).To4()
}
