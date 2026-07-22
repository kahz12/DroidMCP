// Command notifications provides an MCP server for Android notifications via
// Termux:API: post a notification, list the ones this server posted, dismiss a
// notification by id, and read the system Do Not Disturb state. send and
// dismiss have visible side effects but touch no files and run no arbitrary
// code, so — like mcp-clipboard — the server allows key-less dev mode on
// localhost while honouring DROIDMCP_NOTIFICATIONS_KEY / DROIDMCP_API_KEY when
// set. Because posting notifications is user-visible, running with a key is
// recommended outside local development.
//
// Every tool needs the termux-api package and the Termux:API Android app;
// missing pieces surface as an actionable install hint rather than a bare
// exec error. get_dnd_status is best-effort: Termux:API exposes no DND getter,
// so it reads the Android settings provider (global zen_mode) and reports
// clearly when that is not permitted on the device.
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

	server := core.NewDroidServer("mcp-notifications", buildinfo.Version)
	server.APIKey = config.ResolveAPIKey("notifications")
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("send_notification",
		mcp.WithDescription("Post an Android notification via termux-notification. Supply `content` (required) and optionally `title`, `id`, `priority`. Pass an `id` to update or later dismiss a specific notification; without one the system assigns a random id. Returns {sent, id}."),
		mcp.WithString("content", mcp.Required(), mcp.Description("Notification body text")),
		mcp.WithString("title", mcp.Description("Notification title")),
		mcp.WithString("id", mcp.Description("Stable notification id; reuse to update, then pass to dismiss_notification")),
		mcp.WithString("priority", mcp.Description("One of: min, low, default, high, max. Default: default")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleSendNotification)

	s.MCPServer.AddTool(mcp.NewTool("list_notifications",
		mcp.WithDescription("List active notifications via termux-notification-list. Returns the API's JSON array: [{id, tag, key, group, packageName, title, content, when}, …]. Requires the Notification Access permission to be granted to Termux:API."),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleListNotifications)

	s.MCPServer.AddTool(mcp.NewTool("dismiss_notification",
		mcp.WithDescription("Dismiss a notification by id via termux-notification-remove. The id must be one previously passed to send_notification. Returns {dismissed, id}."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Notification id to dismiss")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleDismissNotification)

	s.MCPServer.AddTool(mcp.NewTool("get_dnd_status",
		mcp.WithDescription("Do Not Disturb state read from the Android settings provider (global zen_mode; Termux:API has no DND getter). Returns {dnd_enabled, mode, zen_mode}. May be unavailable on some devices; the error says why."),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleGetDNDStatus)
}
