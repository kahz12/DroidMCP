// Command network provides an MCP server for local network discovery.
// It includes host discovery and port scanning using concurrent Go routines.
package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
)

var cfg *config.Config

func main() {
	var err error
	cfg, err = config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	server := core.NewDroidServer("mcp-network", "1.0.0")
	server.APIKey = config.ResolveAPIKey("network")
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	// scan_network: Uses TCP dial attempts to detect active hosts in a subnet.
	scanNetTool := mcp.NewTool("scan_network",
		mcp.WithDescription("Scan local network for active hosts"),
		mcp.WithString("subnet", mcp.Description("Subnet to scan (e.g., 192.168.1.0/24). If empty, tries to detect local subnet")),
	)
	s.MCPServer.AddTool(scanNetTool, handleScanNetwork)

	// check_ports: Concurrent TCP port scanner for a single host.
	checkPortsTool := mcp.NewTool("check_ports",
		mcp.WithDescription("Scan common ports on a host"),
		mcp.WithString("host", mcp.Required(), mcp.Description("Host to scan (IP or hostname)")),
		mcp.WithString("ports", mcp.Description("Comma-separated list of ports. Default: common ports")),
	)
	s.MCPServer.AddTool(checkPortsTool, handleCheckPorts)
}

func handleScanNetwork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	subnet := req.GetString("subnet", "")
	if subnet == "" {
		subnet = getLocalSubnet()
	}

	if subnet == "" {
		return mcp.NewToolResultError("Could not detect local subnet and none provided"), nil
	}

	logger.Info("Scanning subnet", "subnet", subnet)
	hosts := scanSubnet(subnet)

	return mcp.NewToolResultText(fmt.Sprintf("Active hosts in %s:\n%s", subnet, strings.Join(hosts, "\n"))), nil
}

func handleCheckPorts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	host, err := req.RequireString("host")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	portsStr := req.GetString("ports", "21,22,23,25,53,80,110,135,139,143,443,445,993,995,1723,3306,3389,5900,8080")
	ports := strings.Split(portsStr, ",")

	var wg sync.WaitGroup
	var openPorts []string
	var mu sync.Mutex

	// We use a pool of goroutines to scan ports concurrently.
	for _, port := range ports {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			address := net.JoinHostPort(host, p)
			// Small timeout for local network scanning to keep it fast.
			conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
			if err == nil {
				mu.Lock()
				openPorts = append(openPorts, p)
				mu.Unlock()
				conn.Close()
			}
		}(strings.TrimSpace(port))
	}
	wg.Wait()

	if len(openPorts) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No open ports found on %s", host)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Open ports on %s: %s", host, strings.Join(openPorts, ", "))), nil
}

// getLocalSubnet attempts to identify the current IPv4 subnet of the primary interface.
// It returns the actual CIDR (network address + mask), not a hard-coded /24.
func getLocalSubnet() string {
	addrs, err := net.InterfaceAddrs()
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
			// Skip non-IPv4 masks (e.g., a 4-in-6 IP without a clean v4 mask).
			continue
		}
		network := ip4.Mask(ipnet.Mask)
		return fmt.Sprintf("%s/%d", network.String(), ones)
	}
	return ""
}

// maxScanHosts caps the number of addresses scanSubnet expands a CIDR into so
// that very wide subnets (e.g. /8, /16) cannot spawn millions of dials.
const maxScanHosts = 4096

// scanWorkerLimit bounds concurrent dials to keep file-descriptor and goroutine
// pressure low on resource-constrained Android/Termux devices.
const scanWorkerLimit = 128

// scanSubnet performs a concurrent scan of all hosts inside the given CIDR. It
// honors the supplied mask (no longer hard-coded /24), excludes the network and
// broadcast addresses for masks that have them, and limits concurrency.
// Note: This is a loud scan and may be blocked by some firewalls.
func scanSubnet(subnet string) []string {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil
	}
	ip4 := ipnet.IP.To4()
	if ip4 == nil {
		return nil // IPv4 only for now
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return nil
	}

	hostBits := bits - ones
	var totalAddresses uint64 = 1 << uint(hostBits)
	if totalAddresses > maxScanHosts {
		logger.Info("Subnet too large, capping scan", "cidr", subnet, "limit", maxScanHosts)
		totalAddresses = maxScanHosts
	}

	networkInt := ipv4ToUint32(ip4.Mask(ipnet.Mask))
	// For /31 and /32 every address is a host. For wider masks the first and
	// last addresses are network/broadcast and are skipped.
	var startOffset, endOffset uint64 = 0, totalAddresses
	if hostBits >= 2 {
		startOffset = 1
		endOffset = totalAddresses - 1
	}

	sem := make(chan struct{}, scanWorkerLimit)
	var wg sync.WaitGroup
	var activeHosts []string
	var mu sync.Mutex

	portsToTry := []string{"80", "22", "443"}
	for offset := startOffset; offset < endOffset; offset++ {
		target := uint32ToIPv4(networkInt + uint32(offset)).String()
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()
			// We try ports 80, 22 and 443 as heuristics for active hosts.
			// In a real scenario, ICMP ping might be better, but it requires root in Termux.
			for _, p := range portsToTry {
				conn, err := net.DialTimeout("tcp", net.JoinHostPort(target, p), 150*time.Millisecond)
				if err == nil {
					mu.Lock()
					activeHosts = append(activeHosts, target)
					mu.Unlock()
					conn.Close()
					return
				}
			}
		}(target)
	}
	wg.Wait()
	return activeHosts
}

func ipv4ToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uint32ToIPv4(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n)).To4()
}
