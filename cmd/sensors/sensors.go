package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// runJSONTool executes a Termux:API command whose stdout is a JSON document and
// passes that document through verbatim. A failed run (non-zero exit, timeout,
// cancellation) returns the full sensorResult as an error so stderr reaches the
// caller; a run whose stdout is not valid JSON is wrapped as {"raw": …} so the
// tool still returns something inspectable.
func runJSONTool(ctx context.Context, req mcp.CallToolRequest, name string, args []string, defTimeout time.Duration) (*mcp.CallToolResult, error) {
	if err := ensureBinaries(name); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res, err := runSensorCmd(ctx, name, args, reqTimeout(req, defTimeout))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		body, _ := json.Marshal(res)
		return mcp.NewToolResultError(string(body)), nil
	}
	return jsonPassthrough(res.Stdout)
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

func handleGetBattery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return runJSONTool(ctx, req, binBattery, nil, defaultExecTimeout)
}

// Valid values for get_location's provider and request arguments. "updates"
// is deliberately absent: it streams fixes forever, which does not fit a
// request/response tool.
var (
	locationProviders = map[string]bool{"gps": true, "network": true, "passive": true}
	locationRequests  = map[string]bool{"once": true, "last": true}
)

func handleGetLocation(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	provider := strings.ToLower(req.GetString("provider", "network"))
	if !locationProviders[provider] {
		return mcp.NewToolResultError(fmt.Sprintf("unknown provider %q (want gps, network or passive)", provider)), nil
	}
	request := strings.ToLower(req.GetString("request", "once"))
	if !locationRequests[request] {
		return mcp.NewToolResultError(fmt.Sprintf("unknown request %q (want once or last)", request)), nil
	}
	return runJSONTool(ctx, req, binLocation, []string{"-p", provider, "-r", request}, locationExecTimeout)
}

func handleGetWifiInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return runJSONTool(ctx, req, binWifiInfo, nil, defaultExecTimeout)
}

func handleGetVolume(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return runJSONTool(ctx, req, binVolume, nil, defaultExecTimeout)
}

// brightnessResult is the JSON shape returned by get_brightness.
type brightnessResult struct {
	Brightness int    `json:"brightness"`
	Auto       bool   `json:"auto"`
	Source     string `json:"source"`
}

const brightnessUnavailableHint = "cannot read screen brightness: the Android settings provider did not return a value (some devices restrict it). Note that Termux:API can only set brightness (termux-brightness), not read it."

func handleGetBrightness(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ensureBinaries(binSettings); err != nil {
		return mcp.NewToolResultError(brightnessUnavailableHint + " (`settings` binary not found)"), nil
	}
	timeout := reqTimeout(req, defaultExecTimeout)

	res, err := runSensorCmd(ctx, binSettings, []string{"get", "system", "screen_brightness"}, timeout)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	level, perr := parseSettingsInt(res.Stdout)
	if res.ExitCode != 0 || res.TimedOut || perr != nil {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		return mcp.NewToolResultError(fmt.Sprintf("%s (got: %s)", brightnessUnavailableHint, detail)), nil
	}

	out := brightnessResult{Brightness: level, Source: "settings"}
	// Auto-brightness mode is informative only; failure to read it does not
	// fail the tool.
	if modeRes, merr := runSensorCmd(ctx, binSettings, []string{"get", "system", "screen_brightness_mode"}, timeout); merr == nil && modeRes.ExitCode == 0 {
		if mode, perr := parseSettingsInt(modeRes.Stdout); perr == nil {
			out.Auto = mode == 1
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
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

// toolBackend maps each MCP tool to the command it shells out to, for the
// availability report.
var toolBackends = []struct {
	Tool    string
	Backend string
}{
	{"get_battery", binBattery},
	{"get_location", binLocation},
	{"get_wifi_info", binWifiInfo},
	{"get_brightness", binSettings},
	{"get_volume", binVolume},
	{"list_sensors", binSensor},
}

// backendStatus is one entry in list_sensors' tools report.
type backendStatus struct {
	Backend   string `json:"backend"`
	Available bool   `json:"available"`
}

func handleListSensors(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tools := make(map[string]backendStatus, len(toolBackends))
	for _, tb := range toolBackends {
		_, err := lookPath(tb.Backend)
		tools[tb.Tool] = backendStatus{Backend: tb.Backend, Available: err == nil}
	}

	out := map[string]any{"tools": tools}

	// Hardware inventory via `termux-sensor -l` when the wrapper is present.
	// Failure to list is reported inline rather than failing the whole tool:
	// the availability map above is still useful on its own.
	if tools["list_sensors"].Available {
		res, err := runSensorCmd(ctx, binSensor, []string{"-l"}, reqTimeout(req, defaultExecTimeout))
		switch {
		case err != nil:
			out["hardware_error"] = err.Error()
		case res.ExitCode != 0 || res.TimedOut:
			detail := strings.TrimSpace(res.Stderr)
			if detail == "" {
				detail = strings.TrimSpace(res.Stdout)
			}
			out["hardware_error"] = detail
		default:
			s := strings.TrimSpace(res.Stdout)
			if json.Valid([]byte(s)) {
				out["hardware"] = json.RawMessage(s)
			} else {
				out["hardware_error"] = "termux-sensor -l returned non-JSON output"
			}
		}
	} else {
		out["hardware_error"] = missingTermuxAPIHint
	}

	body, err := json.Marshal(out)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}
