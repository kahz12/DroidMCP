// Command sensors provides an MCP server exposing Android device sensors and
// status through Termux:API wrappers: battery, location, WiFi connection info,
// screen brightness, volume levels, and the hardware sensor inventory. All
// tools are read-only — nothing on the device is modified — so, like
// mcp-clipboard, the server allows key-less dev mode on localhost while still
// honouring DROIDMCP_SENSORS_KEY / DROIDMCP_API_KEY when set.
//
// Every tool needs the termux-api package and the Termux:API Android app;
// missing pieces surface as an actionable install hint rather than a bare
// exec error. get_brightness is best-effort: Termux:API has no brightness
// getter (termux-brightness only sets), so it reads the Android settings
// provider and reports clearly when that is not permitted on the device.
package main

import (
	"github.com/kahz12/droidmcp/internal/buildinfo"
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

	server := core.NewDroidServer("mcp-sensors", buildinfo.Version)
	server.APIKey = config.ResolveAPIKey("sensors")
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("get_battery",
		mcp.WithDescription("Battery status via termux-battery-status. Returns the API's JSON: health, percentage, plugged, status, temperature, current."),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleGetBattery)

	s.MCPServer.AddTool(mcp.NewTool("get_location",
		mcp.WithDescription("Device location via termux-location. Returns the API's JSON: latitude, longitude, altitude, accuracy, bearing, speed, provider. A GPS fix can take a while outdoors-only hardware; `last` returns the cached fix immediately."),
		mcp.WithString("provider", mcp.Description("Location provider: \"gps\", \"network\", or \"passive\". Default: \"network\"")),
		mcp.WithString("request", mcp.Description("Request kind: \"once\" (fresh fix) or \"last\" (cached, immediate). Default: \"once\"")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 30s, max 120s")),
	), handleGetLocation)

	s.MCPServer.AddTool(mcp.NewTool("get_wifi_info",
		mcp.WithDescription("Current WiFi connection via termux-wifi-connectioninfo. Returns the API's JSON: SSID, BSSID, IP, link speed, RSSI, frequency."),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleGetWifiInfo)

	s.MCPServer.AddTool(mcp.NewTool("get_brightness",
		mcp.WithDescription("Screen brightness read from the Android settings provider (Termux:API has no brightness getter). Returns {brightness (0-255), auto}. May be unavailable on some devices; the error says why."),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleGetBrightness)

	s.MCPServer.AddTool(mcp.NewTool("get_volume",
		mcp.WithDescription("Volume of every audio stream via termux-volume. Returns the API's JSON array: [{stream, volume, max_volume}, …]."),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleGetVolume)

	s.MCPServer.AddTool(mcp.NewTool("list_sensors",
		mcp.WithDescription("Availability of each sensor tool's backing command, plus the device's hardware sensor inventory (termux-sensor -l) when available."),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleListSensors)
}
