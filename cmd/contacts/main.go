// Command contacts provides a read-only MCP server over the Android address
// book via Termux:API (termux-contact-list): search contacts, fetch a single
// contact's details, list groups, and export the address book as JSON or
// vCard. Every tool is read-only and the backend command takes no arguments —
// all filtering happens in memory, so no caller-supplied text ever reaches a
// command line. Like mcp-sensors and mcp-notifications the server allows
// key-less dev mode on localhost while honouring DROIDMCP_CONTACTS_KEY /
// DROIDMCP_API_KEY when set; because the address book is personal data,
// running with a key is recommended outside local development.
//
// Every tool needs the termux-api package and the Termux:API Android app;
// missing pieces surface as an actionable install hint rather than a bare
// exec error. list_groups is a documented stub: Termux:API exposes no
// contact-groups endpoint, so the tool reports that clearly instead of
// pretending to have data.
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

	server := core.NewDroidServer("mcp-contacts", buildinfo.Version)
	server.APIKey = config.ResolveAPIKey("contacts")
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("search_contacts",
		mcp.WithDescription("Search the Android address book via termux-contact-list. Matches `query` as a case-insensitive substring of a contact's name or phone number (digits compared ignoring spaces, dashes and parentheses). Returns {count, contacts:[{name, number}, …]}. All filtering is done in memory; the query never touches a command line."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Text to match against contact name or number")),
		mcp.WithNumber("limit", mcp.Description("Maximum contacts to return. Default 50, max 500")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleSearchContacts)

	s.MCPServer.AddTool(mcp.NewTool("get_contact",
		mcp.WithDescription("Fetch the full record for a specific contact. Provide `name` (exact, case-insensitive) and/or `number` (compared ignoring spaces, dashes and parentheses); at least one is required and, when both are given, both must match. Returns {found, count, contacts:[…]}. Multiple entries may share a name, so an array is always returned."),
		mcp.WithString("name", mcp.Description("Exact contact name (case-insensitive)")),
		mcp.WithString("number", mcp.Description("Exact phone number (formatting ignored)")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleGetContact)

	s.MCPServer.AddTool(mcp.NewTool("list_groups",
		mcp.WithDescription("List contact groups. Termux:API exposes no contact-groups endpoint, so this is a documented stub: it returns {supported:false, groups:[], note} rather than fabricated data. Use search_contacts or export_contacts instead."),
	), handleListGroups)

	s.MCPServer.AddTool(mcp.NewTool("export_contacts",
		mcp.WithDescription("Export the address book (optionally filtered by `query`, same matching as search_contacts) as JSON or vCard 3.0. `format` is `json` (default) or `vcard`. JSON returns {format, count, contacts:[…]}; vCard returns {format, count, vcard:\"BEGIN:VCARD…\"} with the ready-to-save .vcf text."),
		mcp.WithString("format", mcp.Description("Output format: json (default) or vcard")),
		mcp.WithString("query", mcp.Description("Optional filter; matches name or number like search_contacts")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 15s, max 120s")),
	), handleExportContacts)
}
