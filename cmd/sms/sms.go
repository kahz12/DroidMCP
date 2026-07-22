package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultListLimit   = 10
	defaultSearchLimit = 100
	maxLimit           = 500
	maxOffset          = 1_000_000
	maxSimSlot         = 32
	maxBodyRunes       = 4000
)

// validSMSTypes mirrors the message boxes termux-sms-list accepts for -t.
var validSMSTypes = map[string]bool{
	"all": true, "inbox": true, "sent": true, "draft": true,
	"outbox": true, "failed": true, "queued": true,
}

// phoneRe accepts a bare phone number: an optional leading + and at least
// three digits, nothing else. Rejecting spaces and punctuation keeps a
// recipient from being split by termux-sms-send's unquoted expansion and blocks
// any attempt to smuggle option-like tokens into the recipient list.
var phoneRe = regexp.MustCompile(`^\+?[0-9]{3,}$`)

func handleListSMS(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	smsType := strings.ToLower(strings.TrimSpace(req.GetString("type", "all")))
	if smsType == "" {
		smsType = "all"
	}
	if !validSMSTypes[smsType] {
		return mcp.NewToolResultError(fmt.Sprintf("unknown type %q (want all, inbox, sent, draft, outbox, failed or queued)", smsType)), nil
	}
	limit := clampLimit(req.GetInt("limit", defaultListLimit))
	offset := clampOffset(req.GetInt("offset", 0))

	if err := ensureBinaries(binSMSList); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res, err := runCmd(ctx, binSMSList, listArgs(smsType, limit, offset), "", reqTimeout(req, defaultExecTimeout))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		body, _ := json.Marshal(res)
		return mcp.NewToolResultError(string(body)), nil
	}
	return jsonPassthrough(res.Stdout)
}

type searchResult struct {
	Count    int              `json:"count"`
	Messages []map[string]any `json:"messages"`
}

func handleSearchSMS(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := strings.TrimSpace(req.GetString("query", ""))
	number := strings.TrimSpace(req.GetString("number", ""))
	if query == "" && number == "" {
		return mcp.NewToolResultError("provide at least one of `query` or `number`"), nil
	}
	smsType := strings.ToLower(strings.TrimSpace(req.GetString("type", "all")))
	if smsType == "" {
		smsType = "all"
	}
	if !validSMSTypes[smsType] {
		return mcp.NewToolResultError(fmt.Sprintf("unknown type %q (want all, inbox, sent, draft, outbox, failed or queued)", smsType)), nil
	}
	limit := clampLimit(req.GetInt("limit", defaultSearchLimit))

	if err := ensureBinaries(binSMSList); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res, err := runCmd(ctx, binSMSList, listArgs(smsType, limit, 0), "", reqTimeout(req, defaultExecTimeout))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		body, _ := json.Marshal(res)
		return mcp.NewToolResultError(string(body)), nil
	}
	msgs, perr := parseMessages(res.Stdout)
	if perr != nil {
		return mcp.NewToolResultError(perr.Error()), nil
	}
	matched := filterMessages(msgs, query, number)
	return jsonResult(searchResult{Count: len(matched), Messages: matched})
}

type sendResult struct {
	Sent       bool     `json:"sent"`
	Recipients []string `json:"recipients"`
	SimSlot    *int     `json:"sim_slot,omitempty"`
}

func handleSendSMS(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := strings.TrimSpace(req.GetString("number", ""))
	if raw == "" {
		return mcp.NewToolResultError("`number` is required (one or more recipients, comma-separated)"), nil
	}
	joined, nums, err := parseRecipients(raw)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	body := req.GetString("text", "")
	if strings.TrimSpace(body) == "" {
		return mcp.NewToolResultError("`text` is required and must not be empty"), nil
	}
	if utf8.RuneCountInString(body) > maxBodyRunes {
		return mcp.NewToolResultError(fmt.Sprintf("`text` is too long: %d characters, max %d", utf8.RuneCountInString(body), maxBodyRunes)), nil
	}

	// Recipients are validated phone numbers passed as one argv element; the
	// body goes on stdin. Neither ever reaches a shell command line.
	args := []string{"-n", joined}
	var slotPtr *int
	if slot := req.GetInt("sim_slot", -1); slot >= 0 {
		if slot > maxSimSlot {
			return mcp.NewToolResultError(fmt.Sprintf("`sim_slot` out of range: %d (max %d)", slot, maxSimSlot)), nil
		}
		args = append(args, "-s", strconv.Itoa(slot))
		s := slot
		slotPtr = &s
	}

	if err := ensureBinaries(binSMSSend); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res, err := runCmd(ctx, binSMSSend, args, body, reqTimeout(req, defaultExecTimeout))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		errBody, _ := json.Marshal(res)
		return mcp.NewToolResultError(string(errBody)), nil
	}
	// termux-sms-send prints nothing on success; exit 0 is the signal.
	return jsonResult(sendResult{Sent: true, Recipients: nums, SimSlot: slotPtr})
}

// listArgs builds the termux-sms-list argument vector. Every value is either a
// validated enum (type) or an integer we formatted ourselves, so nothing here
// is attacker-controlled free text.
func listArgs(smsType string, limit, offset int) []string {
	return []string{"-t", smsType, "-l", strconv.Itoa(limit), "-o", strconv.Itoa(offset)}
}

// parseRecipients splits a comma-separated recipient string, validates each as
// a phone number, and returns the cleaned comma-joined form plus the slice.
func parseRecipients(raw string) (string, []string, error) {
	var nums []string
	for _, p := range strings.Split(raw, ",") {
		n := strings.TrimSpace(p)
		if n == "" {
			continue
		}
		if !phoneRe.MatchString(n) {
			return "", nil, fmt.Errorf("invalid recipient number %q: expected digits with an optional leading +, comma-separated", n)
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return "", nil, errors.New("`number` must contain at least one recipient")
	}
	return strings.Join(nums, ","), nums, nil
}

// parseMessages decodes the termux-sms-list JSON array. Records are kept as
// generic maps so every field the API returns is preserved in the output. An
// empty body means no messages.
func parseMessages(stdout string) ([]map[string]any, error) {
	s := strings.TrimSpace(stdout)
	if s == "" {
		return []map[string]any{}, nil
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(s), &msgs); err != nil {
		return nil, fmt.Errorf("could not parse termux-sms-list output as JSON: %w", err)
	}
	return msgs, nil
}

// filterMessages keeps messages matching every supplied criterion: `query` is a
// case-insensitive substring of the body, `number` matches the address once
// formatting is stripped from both sides.
func filterMessages(msgs []map[string]any, query, number string) []map[string]any {
	q := strings.ToLower(query)
	qn := normalizeNumber(number)
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if query != "" && !strings.Contains(strings.ToLower(msgString(m, "body")), q) {
			continue
		}
		if number != "" && !strings.Contains(normalizeNumber(msgString(m, "number")), qn) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// msgString reads a field from a decoded message as a string, tolerating the
// numeric or boolean types JSON may hand back.
func msgString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
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
			// drop separators
		}
	}
	return b.String()
}

func clampLimit(n int) int {
	if n <= 0 {
		return defaultListLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func clampOffset(n int) int {
	if n < 0 {
		return 0
	}
	if n > maxOffset {
		return maxOffset
	}
	return n
}

// jsonPassthrough returns stdout as-is when it already is a JSON document, and
// wraps it as {"raw": …} otherwise.
func jsonPassthrough(stdout string) (*mcp.CallToolResult, error) {
	s := strings.TrimSpace(stdout)
	if s != "" && json.Valid([]byte(s)) {
		return mcp.NewToolResultText(s), nil
	}
	body, err := json.Marshal(map[string]string{"raw": s})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}
