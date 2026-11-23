// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"encoding/json"
	"errors"
)

// ChatMessage is an authored text message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MessagePlaceholder inserts a named list of runtime messages into a template.
type MessagePlaceholder struct {
	Name string `json:"name"`
}

// ChatTemplateItem contains exactly one authored message or placeholder.
type ChatTemplateItem struct {
	Message     *ChatMessage
	Placeholder *MessagePlaceholder
}

// ValidateChatTemplateItem validates the authored union.
func ValidateChatTemplateItem(item ChatTemplateItem) error {
	if (item.Message == nil) == (item.Placeholder == nil) {
		return errors.New("chat template item must contain exactly one message or placeholder")
	}
	if item.Placeholder != nil && item.Placeholder.Name == "" {
		return errors.New("message placeholder name must be a non-empty string")
	}
	return nil
}

// MarshalJSON encodes the existing message or placeholder wire representation.
func (item ChatTemplateItem) MarshalJSON() ([]byte, error) {
	if err := ValidateChatTemplateItem(item); err != nil {
		return nil, err
	}
	if item.Message != nil {
		return json.Marshal(item.Message)
	}
	return json.Marshal(map[string]string{"type": "placeholder", "name": item.Placeholder.Name})
}

// UnmarshalJSON decodes an authored message or placeholder.
func (item *ChatTemplateItem) UnmarshalJSON(data []byte) error {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	decoded, err := ParseChatTemplateItem(fields)
	if err != nil {
		return err
	}
	*item = decoded
	return nil
}

// ParseChatTemplateItem decodes an authored message or placeholder from registry data.
func ParseChatTemplateItem(fields map[string]any) (ChatTemplateItem, error) {
	if messageType, _ := fields["type"].(string); messageType == "placeholder" {
		name, ok := fields["name"].(string)
		if !ok || name == "" {
			return ChatTemplateItem{}, errors.New("message placeholder name must be a non-empty string")
		}
		if len(fields) != 2 {
			return ChatTemplateItem{}, errors.New("message placeholder must contain only type and name")
		}
		return ChatTemplateItem{Placeholder: &MessagePlaceholder{Name: name}}, nil
	}
	role, roleOK := fields["role"].(string)
	content, contentOK := fields["content"].(string)
	if !roleOK || !contentOK {
		return ChatTemplateItem{}, errors.New("message role and content must be strings")
	}
	return ChatTemplateItem{Message: &ChatMessage{Role: role, Content: content}}, nil
}
