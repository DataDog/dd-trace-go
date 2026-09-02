// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
)

// ErrPromptAuth is returned when DD_API_KEY is not configured.
var ErrPromptAuth = errors.New("llmobs: DD_API_KEY is required for prompt operations")

// PromptMessage is one message in a managed chat prompt.
type PromptMessage struct {
	Role    string
	Content string
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

var promptVariablePattern = regexp.MustCompile(`\{\{?\s*(\w+)\s*\}\}?`)

// Format renders supplied variables and leaves missing placeholders unchanged.
func (p *ManagedPrompt) Format(variables map[string]any) PromptTemplate {
	render := func(s string) string {
		return promptVariablePattern.ReplaceAllStringFunc(s, func(match string) string {
			name := promptVariablePattern.FindStringSubmatch(match)[1]
			if value, ok := variables[name]; ok {
				return fmt.Sprint(value)
			}
			return match
		})
	}
	if p.template.Messages == nil {
		return PromptTemplate{Text: render(p.template.Text)}
	}
	messages := make([]PromptMessage, len(p.template.Messages))
	for i, message := range p.template.Messages {
		messages[i] = PromptMessage{Role: message.Role, Content: render(message.Content)}
	}
	return PromptTemplate{Messages: messages}
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
	for name, value := range variables {
		annotation.Variables[name] = fmt.Sprint(value)
	}
	if p.template.Messages == nil {
		annotation.Template = p.template.Text
	} else {
		annotation.ChatTemplate = make([]LLMMessage, len(p.template.Messages))
		for i, message := range p.template.Messages {
			annotation.ChatTemplate[i] = LLMMessage{Role: message.Role, Content: message.Content}
		}
	}
	return annotation
}

// GetPrompt retrieves a managed prompt. Exact versions use the registry; otherwise DD_ENV
// uses the application's registered default OpenFeature provider for targeting and A/B selection,
// without creating or initializing a provider, and falls through to /resolve when unavailable.
// Without DD_ENV, the latest registry version is fetched. DD_API_KEY is always required and
// the /resolve fallback also requires DD_APP_KEY. Cache and timeout behavior is configured by
// DD_LLMOBS_PROMPTS_CACHE_TTL, DD_LLMOBS_PROMPTS_FILE_CACHE_ENABLED,
// DD_LLMOBS_PROMPTS_CACHE_DIR, and DD_LLMOBS_PROMPTS_TIMEOUT.
// Go has no agentless Feature Flags source; /resolve provides the server-side fallback. Rendering
// does not track prompts automatically: use Annotation with WithAnnotatedPrompt explicitly.
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
	return copy
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
