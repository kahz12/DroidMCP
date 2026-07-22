package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// validPriorities is the set termux-notification accepts for --priority.
var validPriorities = map[string]bool{
	"min": true, "low": true, "default": true, "high": true, "max": true,
}

type sendResult struct {
	Sent bool   `json:"sent"`
	ID   string `json:"id,omitempty"`
}

func handleSendNotification(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	content := req.GetString("content", "")
	if strings.TrimSpace(content) == "" {
		return mcp.NewToolResultError("`content` is required and must not be empty"), nil
	}

	args := []string{"--content", content}
	if title := req.GetString("title", ""); title != "" {
		args = append(args, "--title", title)
	}
	id := req.GetString("id", "")
	if id != "" {
		args = append(args, "--id", id)
	}
	if priority := req.GetString("priority", ""); priority != "" {
		p := strings.ToLower(priority)
		if !validPriorities[p] {
			return mcp.NewToolResultError(fmt.Sprintf("unknown priority %q (want min, low, default, high or max)", priority)), nil
		}
		args = append(args, "--priority", p)
	}

	if err := ensureBinaries(binNotify); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res, err := runCmd(ctx, binNotify, args, reqTimeout(req, defaultExecTimeout))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		body, _ := json.Marshal(res)
		return mcp.NewToolResultError(string(body)), nil
	}
	// termux-notification prints nothing on success. We can only echo back the
	// id the caller supplied; a system-assigned id is not reported by the tool.
	return jsonResult(sendResult{Sent: true, ID: id})
}

func handleListNotifications(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ensureBinaries(binNotifyList); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res, err := runCmd(ctx, binNotifyList, nil, reqTimeout(req, defaultExecTimeout))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		body, _ := json.Marshal(res)
		return mcp.NewToolResultError(string(body)), nil
	}
	return jsonPassthrough(res.Stdout)
}

type dismissResult struct {
	Dismissed bool   `json:"dismissed"`
	ID        string `json:"id"`
}

func handleDismissNotification(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := strings.TrimSpace(req.GetString("id", ""))
	if id == "" {
		return mcp.NewToolResultError("`id` is required"), nil
	}
	if err := ensureBinaries(binNotifyRemove); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res, err := runCmd(ctx, binNotifyRemove, []string{id}, reqTimeout(req, defaultExecTimeout))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		body, _ := json.Marshal(res)
		return mcp.NewToolResultError(string(body)), nil
	}
	return jsonResult(dismissResult{Dismissed: true, ID: id})
}

// zenModeNames maps Android's global zen_mode integer to a readable label.
var zenModeNames = map[int]string{
	0: "off",
	1: "priority_only",
	2: "total_silence",
	3: "alarms_only",
}

type dndResult struct {
	DNDEnabled bool   `json:"dnd_enabled"`
	Mode       string `json:"mode"`
	ZenMode    int    `json:"zen_mode"`
	Source     string `json:"source"`
}

const dndUnavailableHint = "cannot read Do Not Disturb state: the Android settings provider did not return a value (some devices restrict it). Note that Termux:API has no DND getter; this reads global zen_mode from the settings provider."

func handleGetDNDStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ensureBinaries(binSettings); err != nil {
		return mcp.NewToolResultError(dndUnavailableHint + " (`settings` binary not found)"), nil
	}
	res, err := runCmd(ctx, binSettings, []string{"get", "global", "zen_mode"}, reqTimeout(req, defaultExecTimeout))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	zen, perr := parseSettingsInt(res.Stdout)
	if res.ExitCode != 0 || res.TimedOut || perr != nil {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		return mcp.NewToolResultError(fmt.Sprintf("%s (got: %s)", dndUnavailableHint, detail)), nil
	}

	mode, ok := zenModeNames[zen]
	if !ok {
		mode = "unknown"
	}
	return jsonResult(dndResult{
		DNDEnabled: zen != 0,
		Mode:       mode,
		ZenMode:    zen,
		Source:     "settings",
	})
}

// parseSettingsInt parses the output of `settings get`, which prints either an
// integer or the literal "null" when the key is unset.
func parseSettingsInt(stdout string) (int, error) {
	s := strings.TrimSpace(stdout)
	if s == "" || s == "null" {
		return 0, fmt.Errorf("no value (got %q)", s)
	}
	return strconv.Atoi(s)
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
