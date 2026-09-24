// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// FormattedMessage is a rendered text or tool message. Typed fields are authoritative;
// ExtraFields preserves other provider fields without interpreting them. Empty
// content is omitted for tool messages; optional null metadata is omitted.
type FormattedMessage struct {
	Role        string                     `json:"role"`
	Content     string                     `json:"content"`
	ToolCalls   []FormattedToolCall        `json:"tool_calls,omitzero"`
	ToolResults []FormattedToolResult      `json:"tool_results,omitzero"`
	ToolCallID  string                     `json:"tool_call_id,omitempty"`
	ExtraFields map[string]json.RawMessage `json:"-"`
}

// FormattedToolCall preserves both nested function calls and flat Datadog calls.
// Function is nil for calls that do not contain a nested function object.
type FormattedToolCall struct {
	ID          string                     `json:"id,omitempty"`
	Type        string                     `json:"type,omitempty"`
	Function    *ToolFunction              `json:"function,omitempty"`
	Name        string                     `json:"name,omitempty"`
	Arguments   json.RawMessage            `json:"arguments,omitzero"`
	ToolID      string                     `json:"tool_id,omitempty"`
	ExtraFields map[string]json.RawMessage `json:"-"`
}

// ToolFunction is a nested function call. Arguments is the provider's JSON-encoded
// string; it is not decoded or reformatted by prompt rendering.
type ToolFunction struct {
	Name        string                     `json:"name"`
	Arguments   string                     `json:"arguments"`
	ExtraFields map[string]json.RawMessage `json:"-"`
}

// FormattedToolResult preserves a tool result's arbitrary JSON value and metadata.
type FormattedToolResult struct {
	Result      json.RawMessage            `json:"result,omitzero"`
	Name        string                     `json:"name,omitempty"`
	ToolID      string                     `json:"tool_id,omitempty"`
	Type        string                     `json:"type,omitempty"`
	ExtraFields map[string]json.RawMessage `json:"-"`
}

var (
	formattedMessageKeys    = []string{"role", "content", "tool_calls", "tool_results", "tool_call_id"}
	formattedToolCallKeys   = []string{"id", "type", "function", "name", "arguments", "tool_id"}
	toolFunctionKeys        = []string{"name", "arguments"}
	formattedToolResultKeys = []string{"result", "name", "tool_id", "type"}
)

// MarshalJSON preserves provider extensions and omits empty tool-message content.
func (m FormattedMessage) MarshalJSON() ([]byte, error) {
	type plain FormattedMessage
	content := &m.Content
	if m.Content == "" && (len(m.ToolCalls) > 0 || len(m.ToolResults) > 0) {
		content = nil
	}
	return encodeFormattedFields(struct {
		plain
		Content *string `json:"content,omitempty"`
	}{plain(m), content}, m.ExtraFields, formattedMessageKeys)
}

// UnmarshalJSON decodes typed fields without dropping provider extensions.
func (m *FormattedMessage) UnmarshalJSON(data []byte) error {
	type plain FormattedMessage
	var decoded FormattedMessage
	wire := struct {
		*plain
		Content *string `json:"content"`
	}{plain: (*plain)(&decoded)}
	var err error
	decoded.ExtraFields, err = decodeFormattedFields(data, &wire, formattedMessageKeys, "role")
	if err != nil {
		return err
	}
	if wire.Content != nil {
		decoded.Content = *wire.Content
	} else if len(decoded.ToolCalls) == 0 && len(decoded.ToolResults) == 0 {
		return errors.New("runtime message must contain text or tool content")
	}
	var kind string
	_ = json.Unmarshal(decoded.ExtraFields["type"], &kind)
	if kind == "placeholder" {
		return errors.New("runtime messages cannot contain placeholders")
	}
	*m = decoded
	return nil
}

// MarshalJSON preserves typed fields and provider extensions.
func (c FormattedToolCall) MarshalJSON() ([]byte, error) {
	type plain FormattedToolCall
	return encodeFormattedFields(plain(c), c.ExtraFields, formattedToolCallKeys)
}

// UnmarshalJSON validates modeled fields while preserving opaque tool kinds.
func (c *FormattedToolCall) UnmarshalJSON(data []byte) error {
	type plain FormattedToolCall
	var decoded FormattedToolCall
	var err error
	decoded.ExtraFields, err = decodeFormattedFields(data, (*plain)(&decoded), formattedToolCallKeys)
	if err == nil {
		*c = decoded
	}
	return err
}

// MarshalJSON preserves the function's extension fields.
func (f ToolFunction) MarshalJSON() ([]byte, error) {
	type plain ToolFunction
	return encodeFormattedFields(plain(f), f.ExtraFields, toolFunctionKeys)
}

// UnmarshalJSON requires a string name and arguments for a nested function.
func (f *ToolFunction) UnmarshalJSON(data []byte) error {
	type plain ToolFunction
	var decoded ToolFunction
	var err error
	decoded.ExtraFields, err = decodeFormattedFields(data, (*plain)(&decoded), toolFunctionKeys, "name", "arguments")
	if err == nil {
		*f = decoded
	}
	return err
}

// MarshalJSON preserves typed fields and provider extensions.
func (r FormattedToolResult) MarshalJSON() ([]byte, error) {
	type plain FormattedToolResult
	return encodeFormattedFields(plain(r), r.ExtraFields, formattedToolResultKeys)
}

// UnmarshalJSON preserves arbitrary result values and provider extensions.
func (r *FormattedToolResult) UnmarshalJSON(data []byte) error {
	type plain FormattedToolResult
	var decoded FormattedToolResult
	var err error
	decoded.ExtraFields, err = decodeFormattedFields(data, (*plain)(&decoded), formattedToolResultKeys)
	if err == nil {
		*r = decoded
	}
	return err
}

func encodeFormattedFields(value any, extra map[string]json.RawMessage, known []string) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil || len(extra) == 0 {
		return data, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for key, value := range extra {
		if slices.Contains(known, key) {
			return nil, fmt.Errorf("extra field %q conflicts with a typed field", key)
		}
		fields[key] = value
	}
	return json.Marshal(fields)
}

func decodeFormattedFields(data []byte, value any, known []string, required ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("expected a message or tool object")
	}
	for _, key := range required {
		if raw := fields[key]; len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("field %q is required", key)
		}
	}
	typedFields := make(map[string]json.RawMessage, len(known))
	for _, key := range known {
		if raw, exists := fields[key]; exists {
			typedFields[key] = raw
		}
		delete(fields, key)
	}
	// Decode only exact known keys; encoding/json otherwise matches extension
	// names case-insensitively and can overwrite a typed field.
	typedData, err := json.Marshal(typedFields)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(typedData, value); err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		fields = nil
	}
	return fields, nil
}
