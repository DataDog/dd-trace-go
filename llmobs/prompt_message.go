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

// ContentState distinguishes text (including empty text), JSON null, and omitted content.
type ContentState uint8

const (
	ContentText ContentState = iota
	ContentNull
	ContentAbsent
)

// FormattedMessage is a rendered text or tool message. Typed fields are authoritative;
// ExtraFields preserves other provider fields without interpreting them.
type FormattedMessage struct {
	Role         string                     `json:"role"`
	Content      string                     `json:"content"`
	ContentState ContentState               `json:"-"`
	ToolCalls    []FormattedToolCall        `json:"tool_calls,omitzero"`
	ToolResults  []FormattedToolResult      `json:"tool_results,omitzero"`
	ToolCallID   *string                    `json:"tool_call_id,omitempty"`
	ExtraFields  map[string]json.RawMessage `json:"-"`
	// NullFields emits unset optional fields as null. Remove a name before setting
	// that field. Content uses ContentState instead.
	NullFields []string `json:"-"`
}

// FormattedToolCall preserves both nested function calls and flat Datadog calls.
// Function is nil for calls that do not contain a nested function object.
type FormattedToolCall struct {
	ID          *string                    `json:"id,omitempty"`
	Type        *string                    `json:"type,omitempty"`
	Function    *ToolFunction              `json:"function,omitempty"`
	Name        *string                    `json:"name,omitempty"`
	Arguments   json.RawMessage            `json:"arguments,omitzero"`
	ToolID      *string                    `json:"tool_id,omitempty"`
	ExtraFields map[string]json.RawMessage `json:"-"`
	// NullFields follows the same rules as FormattedMessage.NullFields.
	NullFields []string `json:"-"`
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
	Name        *string                    `json:"name,omitempty"`
	ToolID      *string                    `json:"tool_id,omitempty"`
	Type        *string                    `json:"type,omitempty"`
	ExtraFields map[string]json.RawMessage `json:"-"`
	// NullFields follows the same rules as FormattedMessage.NullFields.
	NullFields []string `json:"-"`
}

var (
	formattedMessageKeys        = []string{"role", "content", "tool_calls", "tool_results", "tool_call_id"}
	formattedMessageNullable    = []string{"tool_calls", "tool_results", "tool_call_id"}
	formattedToolCallKeys       = []string{"id", "type", "function", "name", "arguments", "tool_id"}
	formattedToolCallNullable   = []string{"id", "type", "function", "name", "tool_id"}
	toolFunctionKeys            = []string{"name", "arguments"}
	formattedToolResultKeys     = []string{"result", "name", "tool_id", "type"}
	formattedToolResultNullable = []string{"name", "tool_id", "type"}
)

func (m FormattedMessage) validate() error {
	if m.ContentState > ContentAbsent {
		return errors.New("invalid content state")
	}
	if m.ContentState != ContentText {
		if m.Content != "" {
			return errors.New("null or absent content cannot contain text")
		}
		if len(m.ToolCalls) == 0 && len(m.ToolResults) == 0 {
			return errors.New("runtime message must contain text or tool content")
		}
	}
	var kind string
	_ = json.Unmarshal(m.ExtraFields["type"], &kind)
	if kind == "placeholder" {
		return errors.New("runtime messages cannot contain placeholders")
	}
	return nil
}

// MarshalJSON preserves the message's content state and provider extensions.
func (m FormattedMessage) MarshalJSON() ([]byte, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	type plain FormattedMessage
	fields, err := encodeFormattedFields(plain(m), m.ExtraFields, m.NullFields, formattedMessageKeys, formattedMessageNullable)
	if err != nil {
		return nil, err
	}
	switch m.ContentState {
	case ContentNull:
		fields["content"] = json.RawMessage("null")
	case ContentAbsent:
		delete(fields, "content")
	}
	return json.Marshal(fields)
}

// UnmarshalJSON decodes typed fields without dropping provider extensions.
func (m *FormattedMessage) UnmarshalJSON(data []byte) error {
	type plain FormattedMessage
	var decoded FormattedMessage
	wire := struct {
		*plain
		Content json.RawMessage `json:"content"`
	}{plain: (*plain)(&decoded)}
	var err error
	decoded.ExtraFields, decoded.NullFields, err = decodeFormattedFields(data, &wire, formattedMessageKeys, formattedMessageNullable, "role")
	if err != nil {
		return err
	}
	switch {
	case len(wire.Content) == 0:
		decoded.ContentState = ContentAbsent
	case bytes.Equal(bytes.TrimSpace(wire.Content), []byte("null")):
		decoded.ContentState = ContentNull
	default:
		if err := json.Unmarshal(wire.Content, &decoded.Content); err != nil {
			return fmt.Errorf("runtime message content must be a string or null: %w", err)
		}
	}
	if err := decoded.validate(); err != nil {
		return err
	}
	*m = decoded
	return nil
}

// MarshalJSON preserves typed fields, extensions, and optional JSON nulls.
func (c FormattedToolCall) MarshalJSON() ([]byte, error) {
	type plain FormattedToolCall
	fields, err := encodeFormattedFields(plain(c), c.ExtraFields, c.NullFields, formattedToolCallKeys, formattedToolCallNullable)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

// UnmarshalJSON validates modeled fields while preserving opaque tool kinds.
func (c *FormattedToolCall) UnmarshalJSON(data []byte) error {
	type plain FormattedToolCall
	var decoded FormattedToolCall
	var err error
	decoded.ExtraFields, decoded.NullFields, err = decodeFormattedFields(data, (*plain)(&decoded), formattedToolCallKeys, formattedToolCallNullable)
	if err == nil {
		*c = decoded
	}
	return err
}

// MarshalJSON preserves the function's extension fields.
func (f ToolFunction) MarshalJSON() ([]byte, error) {
	type plain ToolFunction
	fields, err := encodeFormattedFields(plain(f), f.ExtraFields, nil, toolFunctionKeys, nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

// UnmarshalJSON requires a string name and arguments for a nested function.
func (f *ToolFunction) UnmarshalJSON(data []byte) error {
	type plain ToolFunction
	var decoded ToolFunction
	var err error
	decoded.ExtraFields, _, err = decodeFormattedFields(data, (*plain)(&decoded), toolFunctionKeys, nil, "name", "arguments")
	if err == nil {
		*f = decoded
	}
	return err
}

// MarshalJSON preserves typed fields, extensions, and optional JSON nulls.
func (r FormattedToolResult) MarshalJSON() ([]byte, error) {
	type plain FormattedToolResult
	fields, err := encodeFormattedFields(plain(r), r.ExtraFields, r.NullFields, formattedToolResultKeys, formattedToolResultNullable)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

// UnmarshalJSON preserves arbitrary result values and provider extensions.
func (r *FormattedToolResult) UnmarshalJSON(data []byte) error {
	type plain FormattedToolResult
	var decoded FormattedToolResult
	var err error
	decoded.ExtraFields, decoded.NullFields, err = decodeFormattedFields(data, (*plain)(&decoded), formattedToolResultKeys, formattedToolResultNullable)
	if err == nil {
		*r = decoded
	}
	return err
}

func encodeFormattedFields(value any, extra map[string]json.RawMessage, nullFields, known, nullable []string) (map[string]json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
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
	for _, key := range nullFields {
		if !slices.Contains(nullable, key) {
			return nil, fmt.Errorf("field %q cannot be marked null", key)
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("field %q cannot be both set and marked null", key)
		}
		fields[key] = json.RawMessage("null")
	}
	return fields, nil
}

func decodeFormattedFields(data []byte, value any, known, nullable []string, required ...string) (map[string]json.RawMessage, []string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, nil, err
	}
	if fields == nil {
		return nil, nil, errors.New("expected a message or tool object")
	}
	for _, key := range required {
		if raw := fields[key]; len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, nil, fmt.Errorf("field %q is required", key)
		}
	}
	typedFields := make(map[string]json.RawMessage, len(known))
	var nullFields []string
	for _, key := range known {
		if raw, exists := fields[key]; exists {
			typedFields[key] = raw
		}
		if slices.Contains(nullable, key) && bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			nullFields = append(nullFields, key)
		}
		delete(fields, key)
	}
	// Decode only exact known keys; encoding/json otherwise matches extension
	// names case-insensitively and can overwrite a typed field.
	typedData, err := json.Marshal(typedFields)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(typedData, value); err != nil {
		return nil, nil, err
	}
	if len(fields) == 0 {
		fields = nil
	}
	return fields, nullFields, nil
}
