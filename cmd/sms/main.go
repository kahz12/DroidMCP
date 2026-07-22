// Command sms provides an MCP server for Android SMS via Termux:API: list
// stored messages (termux-sms-list), search them in memory, and send a
// message (termux-sms-send).
//
// This is the highest-privilege Termux:API server: send_sms triggers a real,
// billable, irreversible outbound message, and the read tools expose highly
// sensitive content (one-time passcodes, 2FA, private conversations). It
// therefore has NO dev mode — like mcp-termux/mcp-filesystem it refuses to
// start without DROIDMCP_SMS_KEY or DROIDMCP_API_KEY, so nothing else on
// localhost (other apps, adb) can drive it unauthenticated.
//
// send_sms never lets caller text reach a shell: recipients are validated to
// be phone numbers and passed as a single argv element, and the body is fed on
// stdin, never as an argument. Every tool needs the termux-api package and the
// Termux:API Android app plus the SMS permissions; a missing piece surfaces as
// an actionable install hint.
package main

import (
	"os"

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

	// Reading SMS exposes OTP/2FA codes and send_sms dispatches real,
	// billable messages, so there is no dev mode: refuse to start unkeyed.
	apiKey := config.ResolveAPIKey("sms")
	if apiKey == "" {
		logger.Log.Error("mcp-sms requires DROIDMCP_SMS_KEY or DROIDMCP_API_KEY to be set. Refusing to start (it reads OTP/2FA messages and can send real SMS).")
		os.Exit(1)
	}

	server := core.NewDroidServer("mcp-sms", buildinfo.Version)
	server.APIKey = apiKey
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("list_sms",
		mcp.WithDescription("List stored SMS messages via termux-sms-list. Returns the API's JSON array (fields such as threadid, type, read, number, received, body). `type` selects the box (all, inbox, sent, draft, outbox, failed, queued); `limit`/`offset` page the results. Reading messages exposes one-time passcodes and 2FA — treat the output as sensitive."),
		mcp.WithString("type", mcp.Description("Message box: all, inbox, sent, draft, outbox, failed, queued. Default: all")),
		mcp.WithNumber("limit", mcp.Description("Max messages to return. Default 10, max 500")),
		mcp.WithNumber("offset", mcp.Description("Number of messages to skip (paging). Default 0")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleListSMS)

	s.MCPServer.AddTool(mcp.NewTool("search_sms",
		mcp.WithDescription("Search stored SMS messages. Fetches a page via termux-sms-list and filters it in memory: `query` matches the message body (case-insensitive substring) and/or `number` matches the sender/recipient (compared ignoring spaces, dashes and parentheses). At least one of `query`/`number` is required. Returns {count, messages:[…]}. The filters never touch a command line."),
		mcp.WithString("query", mcp.Description("Text to match in the message body (case-insensitive)")),
		mcp.WithString("number", mcp.Description("Phone number to match (formatting ignored)")),
		mcp.WithString("type", mcp.Description("Message box to search: all, inbox, sent, draft, outbox, failed, queued. Default: all")),
		mcp.WithNumber("limit", mcp.Description("Max messages to scan/return. Default 100, max 500")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleSearchSMS)

	s.MCPServer.AddTool(mcp.NewTool("send_sms",
		mcp.WithDescription("Send a real SMS via termux-sms-send. THIS DISPATCHES A BILLABLE, IRREVERSIBLE MESSAGE. `number` is one or more recipients (comma-separated, digits with an optional leading +); `text` is the body (sent over stdin, never as an argument). Optional `sim_slot` picks the SIM. Returns {sent, recipients, sim_slot}. Requires the SEND_SMS permission granted to Termux:API."),
		mcp.WithString("number", mcp.Required(), mcp.Description("Recipient number(s), comma-separated. Digits with optional leading +")),
		mcp.WithString("text", mcp.Required(), mcp.Description("Message body")),
		mcp.WithNumber("sim_slot", mcp.Description("SIM slot to send from (0-based). Optional")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleSendSMS)
}
