package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kahz12/droidmcp/internal/dnsconf"
)

// interfaceInfo describes one local NIC for the network_info tool.
type interfaceInfo struct {
	Name  string   `json:"name"`
	MAC   string   `json:"mac,omitempty"`
	Addrs []string `json:"addrs,omitempty"`
	Up    bool     `json:"up"`
}

// networkInfo is the response shape for the network_info tool.
type networkInfo struct {
	DefaultGateway string          `json:"default_gateway,omitempty"`
	DNSServers     []string        `json:"dns_servers,omitempty"`
	Interfaces     []interfaceInfo `json:"interfaces,omitempty"`
	LocalSubnet    string          `json:"local_subnet,omitempty"`
	Note           string          `json:"note,omitempty"`
}

// procRoutePath is /proc/net/route on Linux/Termux. Tests can override it.
var procRoutePath = "/proc/net/route"

// resolvConfPath is /etc/resolv.conf, or Termux's on Android, which has none.
// Tests can override it.
var resolvConfPath = dnsconf.Path()

// gatherNetworkInfo collects the interface list, the default gateway from
// /proc/net/route and the DNS servers from resolvConfPath. Best effort:
// missing files or unreadable interfaces are reflected as empty fields, not
// errors, so the caller still gets whatever we could find.
func gatherNetworkInfo(ctx context.Context) networkInfo {
	subnet, assumed := localSubnet(ctx)
	out := networkInfo{
		DefaultGateway: defaultGatewayFromProc(),
		DNSServers:     dnsServersFromResolv(),
		Interfaces:     listInterfaces(),
		LocalSubnet:    subnet,
	}
	if assumed {
		out.Note = assumedSubnetNote
	}
	return out
}

// defaultGatewayFromProc reads /proc/net/route and returns the gateway for
// the first 0.0.0.0/0 entry (lowest metric is good enough for our purpose).
// Returns "" when the file is missing or no default route is present.
func defaultGatewayFromProc() string {
	f, err := os.Open(procRoutePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		// Field 1 is destination (hex, little-endian). Default route is all zeros.
		if fields[1] != "00000000" {
			continue
		}
		// Field 2 is the gateway, also hex little-endian.
		raw, err := hex.DecodeString(fields[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		// /proc/net/route prints the gateway as a hex little-endian word,
		// so the bytes come out in reverse-octet order.
		return net.IPv4(raw[3], raw[2], raw[1], raw[0]).String()
	}
	return ""
}

// dnsServersFromResolv returns the nameserver lines from resolvConfPath.
func dnsServersFromResolv() []string {
	return dnsconf.Nameservers(resolvConfPath)
}

// listInterfaces enumerates local NICs with their MAC and IP addresses.
// Loopback is included so the caller can sanity-check that something is up.
func listInterfaces() []interfaceInfo {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]interfaceInfo, 0, len(ifaces))
	for _, iface := range ifaces {
		info := interfaceInfo{
			Name: iface.Name,
			MAC:  iface.HardwareAddr.String(),
			Up:   iface.Flags&net.FlagUp != 0,
		}
		addrs, err := iface.Addrs()
		if err == nil {
			for _, a := range addrs {
				info.Addrs = append(info.Addrs, a.String())
			}
		}
		out = append(out, info)
	}
	return out
}

// interfaceAddrs is net.InterfaceAddrs. Tests replace it to play Android 11+,
// which refuses apps the netlink socket it needs.
var interfaceAddrs = net.InterfaceAddrs

// wifiInfo runs termux-wifi-connectioninfo and returns its JSON. Tests replace
// it.
var wifiInfo = func(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, wifiInfoTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "termux-wifi-connectioninfo")
	// Stop waiting for stdout shortly after the timeout: the wrapper's
	// termux-api child can outlive it and keep the pipe open.
	cmd.WaitDelay = 2 * time.Second
	return cmd.Output()
}

// wifiInfoTimeout bounds termux-wifi-connectioninfo, which waits forever when
// the Termux:API app is not installed.
const wifiInfoTimeout = 5 * time.Second

// assumedPrefix is the mask localSubnet assumes around the Wi-Fi address: that
// of nearly every home and small-office network.
const assumedPrefix = 24

const assumedSubnetNote = "the subnet is the /24 around the Wi-Fi address: Android hides network interfaces and their masks from apps, so the real one may be wider or narrower"

// localSubnet returns the subnet to scan when none is given: the first
// non-loopback IPv4 interface's, with the mask the kernel reports. Android 11+
// hides interfaces from apps, so there it falls back to the /24 around the
// Wi-Fi address Termux:API reports, and assumed is true. It returns "" when
// neither works: Android without Termux:API, or off Wi-Fi (mobile data has no
// LAN to scan).
func localSubnet(ctx context.Context) (subnet string, assumed bool) {
	if s := interfaceSubnet(); s != "" {
		return s, false
	}
	out, err := wifiInfo(ctx)
	if err != nil {
		return "", false
	}
	ip := wifiIPv4(out)
	if ip == nil {
		return "", false
	}
	mask := net.CIDRMask(assumedPrefix, 32)
	return (&net.IPNet{IP: ip.Mask(mask), Mask: mask}).String(), true
}

// wifiIPv4 returns the address of a completed Wi-Fi connection from
// termux-wifi-connectioninfo's JSON, or nil when there is none.
func wifiIPv4(out []byte) net.IP {
	var info struct {
		IP              string `json:"ip"`
		SupplicantState string `json:"supplicant_state"`
	}
	if json.Unmarshal(out, &info) != nil || info.SupplicantState != "COMPLETED" {
		return nil
	}
	ip := net.ParseIP(info.IP).To4()
	if ip == nil || ip.IsUnspecified() {
		return nil
	}
	return ip
}

// interfaceSubnet returns the CIDR of the first non-loopback IPv4 interface,
// using the actual mask exposed by the kernel (not a hard-coded /24).
func interfaceSubnet() string {
	addrs, err := interfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addrs {
		ipnet, ok := address.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		ones, bits := ipnet.Mask.Size()
		if bits != 32 {
			continue
		}
		network := ip4.Mask(ipnet.Mask)
		return network.String() + "/" + intToStr(ones)
	}
	return ""
}

// intToStr formats i in decimal.
func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
