// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcp

import "slices"

const TelemetryKey = "telemetry"

const IntentKey = "intent"

const IntentPrompt string = "Briefly describe the wider context task, and why this tool was chosen. Omit argument values, PII/secrets. Use English."

const MCPToolTag = "mcp_tool"
const MCPToolKindTag = "mcp_tool_kind"
const MCPMethodTag = "mcp_method"
const MCPSessionIDTag = "mcp_session_id"
const MCPClientNameTag = "client_name"
const MCPClientVersionTag = "client_version"

// IsModelCallable reports whether a tool's _meta permits the model to call it.
// Absent _meta.ui.visibility means model-callable (MCP Apps spec default); a
// visibility list that omits "model" means UI-only and the model should not
// be asked to supply telemetry/intent for it.
//
// Callers pass the raw _meta map regardless of SDK wrapping: mark3labs/mcp-go
// stores it as mcp.Meta.AdditionalFields; modelcontextprotocol/go-sdk uses
// mcp.Meta directly (which is map[string]any).
//
// See https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/SEP-1865.md
func IsModelCallable(meta map[string]any) bool {
	if meta == nil {
		return true
	}
	uiMap, ok := meta["ui"].(map[string]any)
	if !ok {
		return true
	}
	raw, ok := uiMap["visibility"]
	if !ok {
		return true
	}
	switch v := raw.(type) {
	case []string:
		return slices.Contains(v, "model")
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == "model" {
				return true
			}
		}
		return false
	default:
		return true
	}
}
