package main

import "github.com/mark3labs/mcp-go/mcp"

// apiTools returns the termux-api wrappers. They bypass the allowlist because
// the operator opted into them by deploying mcp-termux; the allowlist only
// limits the generic shell, not these dedicated tools.
func apiTools() []ToolSpec {
	return []ToolSpec{
		batteryStatusTool(),
		locationTool(),
		notificationTool(),
		toastTool(),
		smsSendTool(),
		ttsSpeakTool(),
	}
}

func batteryStatusTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("termux_battery_status",
			mcp.WithDescription("Get battery status (level, plugged, health)"),
			mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout")),
		),
		Handler: handleBatteryStatus,
	}
}

func locationTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("termux_location",
			mcp.WithDescription("Get device location"),
			mcp.WithString("provider", mcp.Description("Location provider: gps, network, passive (default: gps)")),
			mcp.WithString("request", mcp.Description("Request type: once, last, updates (default: once)")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout")),
		),
		Handler: handleLocation,
	}
}

func notificationTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("termux_notification",
			mcp.WithDescription("Show an Android notification"),
			mcp.WithString("title", mcp.Description("Notification title")),
			mcp.WithString("content", mcp.Description("Notification body")),
			mcp.WithString("id", mcp.Description("Optional notification id (replace previous)")),
		),
		Handler: handleNotification,
	}
}

func toastTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("termux_toast",
			mcp.WithDescription("Show a short on-screen toast"),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to display")),
		),
		Handler: handleToast,
	}
}

func smsSendTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("termux_sms_send",
			mcp.WithDescription("Send an SMS. Requires SMS permission for Termux:API."),
			mcp.WithString("number", mcp.Required(), mcp.Description("Recipient phone number")),
			mcp.WithString("text", mcp.Required(), mcp.Description("Message body")),
		),
		Handler: handleSMSSend,
	}
}

func ttsSpeakTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("termux_tts_speak",
			mcp.WithDescription("Speak text via the device TTS engine"),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to speak")),
			mcp.WithString("language", mcp.Description("BCP47 tag, e.g. en-US")),
			mcp.WithNumber("rate", mcp.Description("Speech rate (1.0 = normal)")),
			mcp.WithNumber("pitch", mcp.Description("Speech pitch (1.0 = normal)")),
		),
		Handler: handleTTSSpeak,
	}
}
