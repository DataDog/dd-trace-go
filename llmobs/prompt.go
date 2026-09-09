// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

// ErrPromptAuth is returned when HTTP retrieval requires DD_API_KEY and none is configured.
var ErrPromptAuth = errors.New("llmobs: DD_API_KEY is required for prompt operations")

// PromptMessage is one message in a managed chat prompt.
type PromptMessage struct {
	Role    string
	Content string
	// Type and Name represent an authored message placeholder when Type is "placeholder".
	Type string
	Name string
	// AdditionalFields preserves provider-specific runtime message fields without interpreting them.
	AdditionalFields map[string]any
}

// MarshalJSON preserves provider-specific message fields at their original level.
func (m PromptMessage) MarshalJSON() ([]byte, error) {
	return json.Marshal(promptMessageMap(m))
}

// UnmarshalJSON restores a message or authored message placeholder.
func (m *PromptMessage) UnmarshalJSON(data []byte) error {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	message, err := promptMessage(fields)
	if err != nil {
		return err
	}
	*m = message
	return nil
}

// PromptTemplate is either text or chat content. A non-nil Messages slice,
// including an empty slice, identifies a chat template.
type PromptTemplate struct {
	Text     string
	Messages []PromptMessage
}

// PromptFallback is used when a managed prompt cannot be fetched.
type PromptFallback struct {
	Template PromptTemplate
	Version  string
}

// PromptSource identifies where a managed prompt came from.
type PromptSource string

const (
	PromptSourceRegistry    PromptSource = "registry"
	PromptSourceCache       PromptSource = "cache"
	PromptSourceFallback    PromptSource = "fallback"
	PromptSourceFeatureFlag PromptSource = "ff"
	PromptSourceResolve     PromptSource = "resolve"
)

// ManagedPrompt is an immutable prompt retrieved from the Datadog Prompt Registry.
type ManagedPrompt struct {
	id                string
	version           string
	source            PromptSource
	template          PromptTemplate
	promptUUID        string
	promptVersionUUID string
}

// ID returns the prompt identifier.
func (p *ManagedPrompt) ID() string { return p.id }

// Version returns the prompt version.
func (p *ManagedPrompt) Version() string { return p.version }

// Source returns the source used to retrieve the prompt.
func (p *ManagedPrompt) Source() PromptSource { return p.source }

// Template returns an unrendered copy of the prompt template.
func (p *ManagedPrompt) Template() PromptTemplate { return copyPromptTemplate(p.template) }

var promptVariablePattern = regexp.MustCompile(`\{\{\s*(\w+)\s*\}\}|\{\s*(\w+)\s*\}`)

// Format renders supplied variables. Missing text variables remain unchanged;
// missing or malformed message-placeholder values return an error.
func (p *ManagedPrompt) Format(variables map[string]any) (PromptTemplate, error) {
	render := func(s string) string {
		var rendered strings.Builder
		last := 0
		for _, match := range promptVariablePattern.FindAllStringSubmatchIndex(s, -1) {
			if match[0] > 0 && s[match[0]-1] == '{' || match[1] < len(s) && s[match[1]] == '}' {
				continue
			}
			start, end := match[2], match[3]
			if start == -1 {
				start, end = match[4], match[5]
			}
			value, ok := variables[s[start:end]]
			if !ok {
				continue
			}
			rendered.WriteString(s[last:match[0]])
			rendered.WriteString(fmt.Sprint(value))
			last = match[1]
		}
		if last == 0 {
			return s
		}
		rendered.WriteString(s[last:])
		return rendered.String()
	}
	if p.template.Messages == nil {
		return PromptTemplate{Text: render(p.template.Text)}, nil
	}
	messages := make([]PromptMessage, 0, len(p.template.Messages))
	for _, message := range p.template.Messages {
		if message.Type != "placeholder" {
			copy := copyPromptMessage(message)
			copy.Content = render(message.Content)
			messages = append(messages, copy)
			continue
		}
		value, ok := variables[message.Name]
		if !ok {
			return PromptTemplate{}, fmt.Errorf("llmobs: missing message placeholder variable %q", message.Name)
		}
		inserted, err := runtimePromptMessages(value)
		if err != nil {
			return PromptTemplate{}, fmt.Errorf("llmobs: invalid message placeholder variable %q: %w", message.Name, err)
		}
		messages = append(messages, inserted...)
	}
	return PromptTemplate{Messages: messages}, nil
}

// Annotation converts the managed prompt to the existing explicit span annotation shape.
// Pass the result to WithAnnotatedPrompt; formatting never tracks prompts automatically.
func (p *ManagedPrompt) Annotation(variables map[string]any) Prompt {
	annotation := Prompt{
		ID:                p.id,
		Version:           p.version,
		PromptUUID:        p.promptUUID,
		PromptVersionUUID: p.promptVersionUUID,
		Variables:         make(map[string]string, len(variables)),
	}
	placeholderNames := make(map[string]struct{})
	for _, message := range p.template.Messages {
		if message.Type == "placeholder" {
			placeholderNames[message.Name] = struct{}{}
		}
	}
	for name, value := range variables {
		if _, structural := placeholderNames[name]; structural {
			continue
		}
		annotation.Variables[name] = fmt.Sprint(value)
	}
	if p.template.Messages == nil {
		annotation.Template = p.template.Text
	} else {
		authored := make([]map[string]any, len(p.template.Messages))
		annotation.ChatTemplate = make([]LLMMessage, 0, len(p.template.Messages))
		for i, message := range p.template.Messages {
			authored[i] = promptMessageMap(message)
			if message.Type != "placeholder" {
				annotation.ChatTemplate = append(annotation.ChatTemplate, LLMMessage{Role: message.Role, Content: message.Content})
			}
		}
		annotation = illmobs.WithManagedPromptChatTemplate(annotation, authored)
	}
	return annotation
}

// GetPrompt retrieves a managed prompt by ID.
// Options may select an exact version, provide targeting context, or define a fallback.
func GetPrompt(ctx context.Context, promptID string, opts ...GetPromptOption) (*ManagedPrompt, error) {
	config := getPromptConfig{}
	for _, option := range opts {
		option(&config)
	}
	return globalPromptManager().get(ctx, promptID, config)
}

// GetPromptOption configures GetPrompt.
type GetPromptOption func(*getPromptConfig)

type getPromptConfig struct {
	version      *int
	targetingKey string
	attributes   map[string]any
	fallback     *PromptFallback
	fallbackFunc func() (PromptFallback, error)
}

// WithPromptVersion selects one exact registry version and ignores targeting options.
func WithPromptVersion(version int) GetPromptOption {
	return func(config *getPromptConfig) { config.version = &version }
}

// WithPromptTargetingKey sets the OpenFeature and /resolve targeting key.
// OpenFeature evaluation and exposure reporting require a side-effect import of
// github.com/DataDog/dd-trace-go/v2/openfeature and DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED=true.
func WithPromptTargetingKey(targetingKey string) GetPromptOption {
	return func(config *getPromptConfig) { config.targetingKey = targetingKey }
}

// WithPromptTargetingAttributes sets a shallow snapshot of flat targeting attributes.
func WithPromptTargetingAttributes(attributes map[string]any) GetPromptOption {
	return func(config *getPromptConfig) {
		config.attributes = maps.Clone(attributes)
	}
}

// WithPromptFallback sets a static fallback.
func WithPromptFallback(fallback PromptFallback) GetPromptOption {
	return func(config *getPromptConfig) {
		copy := fallback
		copy.Template = copyPromptTemplate(fallback.Template)
		config.fallback = &copy
		config.fallbackFunc = nil
	}
}

// WithPromptFallbackFunc sets a fallback evaluated only after retrieval and cache failure.
func WithPromptFallbackFunc(fallback func() (PromptFallback, error)) GetPromptOption {
	return func(config *getPromptConfig) {
		config.fallback = nil
		config.fallbackFunc = fallback
	}
}

func copyPromptTemplate(template PromptTemplate) PromptTemplate {
	copy := template
	copy.Messages = slices.Clone(template.Messages)
	for i := range copy.Messages {
		copy.Messages[i] = copyPromptMessage(copy.Messages[i])
	}
	return copy
}

func copyPromptMessage(message PromptMessage) PromptMessage {
	message.AdditionalFields = maps.Clone(message.AdditionalFields)
	return message
}

func promptMessage(fields map[string]any) (PromptMessage, error) {
	additional := maps.Clone(fields)
	if messageType, _ := fields["type"].(string); messageType == "placeholder" {
		name, ok := fields["name"].(string)
		if !ok || name == "" {
			return PromptMessage{}, errors.New("message placeholder name must be a non-empty string")
		}
		delete(additional, "type")
		delete(additional, "name")
		if len(additional) == 0 {
			additional = nil
		}
		return PromptMessage{Type: messageType, Name: name, AdditionalFields: additional}, nil
	}
	role, roleOK := fields["role"].(string)
	content, contentOK := fields["content"].(string)
	if !roleOK || !contentOK {
		return PromptMessage{}, errors.New("message role and content must be strings")
	}
	delete(additional, "role")
	delete(additional, "content")
	if len(additional) == 0 {
		additional = nil
	}
	return PromptMessage{Role: role, Content: content, AdditionalFields: additional}, nil
}

func promptMessageMap(message PromptMessage) map[string]any {
	fields := maps.Clone(message.AdditionalFields)
	if fields == nil {
		fields = make(map[string]any)
	}
	if message.Type == "placeholder" {
		fields["type"] = message.Type
		fields["name"] = message.Name
		delete(fields, "role")
		delete(fields, "content")
	} else {
		fields["role"] = message.Role
		fields["content"] = message.Content
	}
	return fields
}

func runtimePromptMessages(value any) ([]PromptMessage, error) {
	messages, ok := value.([]PromptMessage)
	if !ok {
		return nil, errors.New("expected a message list")
	}
	copies := make([]PromptMessage, len(messages))
	for i, message := range messages {
		if message.Type != "" {
			return nil, errors.New("runtime messages cannot contain placeholders")
		}
		copies[i] = copyPromptMessage(message)
	}
	return copies, nil
}

func newManagedPrompt(id, version string, source PromptSource, template PromptTemplate, promptUUID, versionUUID string) (*ManagedPrompt, error) {
	if template.Text != "" && template.Messages != nil {
		return nil, errors.New("llmobs: prompt template cannot contain both text and messages")
	}
	return &ManagedPrompt{id: id, version: version, source: source, template: copyPromptTemplate(template), promptUUID: promptUUID, promptVersionUUID: versionUUID}, nil
}

func (p *ManagedPrompt) withSource(source PromptSource) *ManagedPrompt {
	copy := *p
	copy.source = source
	copy.template = copyPromptTemplate(p.template)
	return &copy
}
