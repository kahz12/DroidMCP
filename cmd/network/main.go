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
func getLocalSubnet() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				// Assuming a /24 subnet for simplicity in local network discovery.
				ip := ipnet.IP.To4()
				return fmt.Sprintf("%d.%d.%d.0/24", ip[0], ip[1], ip[2])
			}
		}
	}
	return ""
}

// scanSubnet performs a basic concurrent scan of a /24 subnet by attempting TCP connections.
// Note: This is a loud scan and may be blocked by some firewalls.
func scanSubnet(subnet string) []string {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil
	}

	var wg sync.WaitGroup
	var activeHosts []string
	var mu sync.Mutex

	ip := ipnet.IP.To4()
	for i := 1; i < 255; i++ {
		wg.Add(1)
		go func(lastByte int) {
			defer wg.Done()
			target := fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], lastByte)
			// We try ports 80 and 22 as heuristics for active hosts.
			// In a real scenario, ICMP ping might be better, but it requires root in Termux.
			portsToTry := []string{"80", "22", "443"}
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
		}(i)
	}
	wg.Wait()
	return activeHosts
}
