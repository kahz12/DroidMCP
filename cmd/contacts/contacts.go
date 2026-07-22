package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultLimit = 50
	maxLimit     = 500
)

// contact mirrors the objects termux-contact-list emits: the Termux:API
// ContactList endpoint reports a name and a single phone number per entry.
type contact struct {
	Name   string `json:"name"`
	Number string `json:"number"`
}

// fetchContacts runs termux-contact-list and parses its JSON array. On any
// failure it returns a ready-to-send error result (second return non-nil) so
// each handler can `if errRes != nil { return errRes, nil }`.
func fetchContacts(ctx context.Context, req mcp.CallToolRequest) ([]contact, *mcp.CallToolResult) {
	if err := ensureBinaries(binContactList); err != nil {
		return nil, mcp.NewToolResultError(err.Error())
	}
	res, err := runCmd(ctx, binContactList, nil, reqTimeout(req, defaultExecTimeout))
	if err != nil {
		return nil, mcp.NewToolResultError(err.Error())
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		body, _ := json.Marshal(res)
		return nil, mcp.NewToolResultError(string(body))
	}
	contacts, perr := parseContacts(res.Stdout)
	if perr != nil {
		return nil, mcp.NewToolResultError(perr.Error())
	}
	return contacts, nil
}

// parseContacts decodes the termux-contact-list JSON array. An empty body is
// treated as an empty list (the wrapper prints nothing when there are none).
func parseContacts(stdout string) ([]contact, error) {
	s := strings.TrimSpace(stdout)
	if s == "" {
		return []contact{}, nil
	}
	var contacts []contact
	if err := json.Unmarshal([]byte(s), &contacts); err != nil {
		return nil, fmt.Errorf("could not parse termux-contact-list output as JSON: %w", err)
	}
	return contacts, nil
}

type searchResult struct {
	Count    int       `json:"count"`
	Contacts []contact `json:"contacts"`
}

func handleSearchContacts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := strings.TrimSpace(req.GetString("query", ""))
	if query == "" {
		return mcp.NewToolResultError("`query` is required and must not be empty"), nil
	}
	limit := clampLimit(req.GetInt("limit", defaultLimit))

	contacts, errRes := fetchContacts(ctx, req)
	if errRes != nil {
		return errRes, nil
	}

	matched := filterContacts(contacts, query)
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return jsonResult(searchResult{Count: len(matched), Contacts: matched})
}

type getResult struct {
	Found    bool      `json:"found"`
	Count    int       `json:"count"`
	Contacts []contact `json:"contacts"`
}

func handleGetContact(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := strings.TrimSpace(req.GetString("name", ""))
	number := strings.TrimSpace(req.GetString("number", ""))
	if name == "" && number == "" {
		return mcp.NewToolResultError("provide at least one of `name` or `number`"), nil
	}

	contacts, errRes := fetchContacts(ctx, req)
	if errRes != nil {
		return errRes, nil
	}

	wantName := strings.ToLower(name)
	wantNumber := normalizeNumber(number)
	var matched []contact
	for _, c := range contacts {
		if name != "" && strings.ToLower(strings.TrimSpace(c.Name)) != wantName {
			continue
		}
		if number != "" && normalizeNumber(c.Number) != wantNumber {
			continue
		}
		matched = append(matched, c)
	}
	if matched == nil {
		matched = []contact{}
	}
	return jsonResult(getResult{Found: len(matched) > 0, Count: len(matched), Contacts: matched})
}

type groupsResult struct {
	Supported bool     `json:"supported"`
	Groups    []string `json:"groups"`
	Note      string   `json:"note"`
}

func handleListGroups(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return jsonResult(groupsResult{
		Supported: false,
		Groups:    []string{},
		Note:      "Termux:API exposes no contact-groups endpoint, so grouping is not available. Use search_contacts or export_contacts instead.",
	})
}

type exportJSONResult struct {
	Format   string    `json:"format"`
	Count    int       `json:"count"`
	Contacts []contact `json:"contacts"`
}

type exportVCardResult struct {
	Format string `json:"format"`
	Count  int    `json:"count"`
	VCard  string `json:"vcard"`
}

func handleExportContacts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	format := strings.ToLower(strings.TrimSpace(req.GetString("format", "json")))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "vcard" {
		return mcp.NewToolResultError(fmt.Sprintf("unknown format %q (want json or vcard)", format)), nil
	}

	contacts, errRes := fetchContacts(ctx, req)
	if errRes != nil {
		return errRes, nil
	}
	if query := strings.TrimSpace(req.GetString("query", "")); query != "" {
		contacts = filterContacts(contacts, query)
	}
	if contacts == nil {
		contacts = []contact{}
	}

	if format == "vcard" {
		return jsonResult(exportVCardResult{Format: "vcard", Count: len(contacts), VCard: buildVCard(contacts)})
	}
	return jsonResult(exportJSONResult{Format: "json", Count: len(contacts), Contacts: contacts})
}

// filterContacts keeps contacts whose name contains the query (case-insensitive)
// or whose number matches once formatting is stripped from both sides.
func filterContacts(contacts []contact, query string) []contact {
	q := strings.ToLower(strings.TrimSpace(query))
	qNum := normalizeNumber(query)
	out := make([]contact, 0, len(contacts))
	for _, c := range contacts {
		if strings.Contains(strings.ToLower(c.Name), q) {
			out = append(out, c)
			continue
		}
		if qNum != "" && strings.Contains(normalizeNumber(c.Number), qNum) {
			out = append(out, c)
		}
	}
	return out
}

// normalizeNumber strips spaces, dashes, dots and parentheses so numbers that
// differ only in formatting compare equal. A leading "+" is preserved.
func normalizeNumber(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+':
			b.WriteRune(r)
		default:
			// drop spaces, dashes, dots, parentheses and any other separator
		}
	}
	return b.String()
}

func clampLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

// buildVCard serialises contacts to concatenated vCard 3.0 records. Values are
// escaped per RFC 6350 so a name or number cannot break the record structure.
func buildVCard(contacts []contact) string {
	var b strings.Builder
	for _, c := range contacts {
		b.WriteString("BEGIN:VCARD\r\n")
		b.WriteString("VERSION:3.0\r\n")
		b.WriteString("FN:")
		b.WriteString(vcardEscape(c.Name))
		b.WriteString("\r\n")
		if strings.TrimSpace(c.Number) != "" {
			b.WriteString("TEL:")
			b.WriteString(vcardEscape(c.Number))
			b.WriteString("\r\n")
		}
		b.WriteString("END:VCARD\r\n")
	}
	return b.String()
}

// vcardEscape escapes the characters that are structural in a vCard value:
// backslash, comma, semicolon and newlines (CR/LF collapse to the \n escape).
func vcardEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`;`, `\;`,
		`,`, `\,`,
		"\r\n", `\n`,
		"\r", `\n`,
		"\n", `\n`,
	)
	return r.Replace(s)
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}
