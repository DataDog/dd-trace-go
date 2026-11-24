// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcp

import (
	"encoding/json"
	"slices"
)

const TelemetryKey = "telemetry"

const IntentKey = "intent"

const IntentPrompt string = "Briefly describe the wider context task, and why this tool was chosen. Omit argument values, PII/secrets. Use English."

const MCPToolTag = "mcp_tool"
const MCPToolKindTag = "mcp_tool_kind"
const MCPMethodTag = "mcp_method"
const MCPSessionIDTag = "mcp_session_id"
const MCPClientNameTag = "client_name"
const MCPClientVersionTag = "client_version"

const MCPClientInfoNameTag = "mcp_client_name"
const MCPClientInfoTitleTag = "mcp_client_title"
const MCPClientInfoVersionTag = "mcp_client_version"

// MetaKeyClientInfo is the request _meta key that carries the client's identity on
// every request of the 2026-07-28 protocol.
const MetaKeyClientInfo = "io.modelcontextprotocol/clientInfo"

// MaxClientInfoTagLength caps client-supplied identity values, which are repeated on
// every tool call span.
const MaxClientInfoTagLength = 256

// ClientInfoTags returns the mcp_client_* tags for a client's self-reported identity,
// omitting empty values and truncating long ones.
func ClientInfoTags(name, title, version string) map[string]string {
	tags := make(map[string]string, 3)
	for key, value := range map[string]string{
		MCPClientInfoNameTag:    name,
		MCPClientInfoTitleTag:   title,
		MCPClientInfoVersionTag: version,
	} {
		if value == "" {
			continue
		}
		if runes := []rune(value); len(runes) > MaxClientInfoTagLength {
			value = string(runes[:MaxClientInfoTagLength])
		}
		tags[key] = value
	}
	return tags
}

// ClientInfoTagsFromValue returns the mcp_client_* tags for a client identity of any
// shape that encodes as an MCP Implementation object: a decoded _meta entry, or an SDK
// Implementation struct. Decoding through JSON keeps this independent of which fields
// a given SDK version declares.
func ClientInfoTagsFromValue(value any) map[string]string {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var info struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &info) != nil {
		return nil
	}
	return ClientInfoTags(info.Name, info.Title, info.Version)
}

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
