// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package genaitrace

import (
	"context"
	"iter"

	"google.golang.org/genai"
)

// Chats wraps *genai.Client.Chats with LLM Observability tracing.
type Chats struct {
	c        *genai.Chats
	provider string
}

// Create creates a new chat session. The returned *Chat is a tracing wrapper;
// its SendMessage and SendMessageStream methods produce LLM spans.
func (c *Chats) Create(ctx context.Context, model string, config *genai.GenerateContentConfig, history []*genai.Content) (*Chat, error) {
	if c == nil || c.c == nil {
		return nil, errNilClient
	}
	chat, err := c.c.Create(ctx, model, config, history)
	if err != nil {
		return nil, err
	}
	return &Chat{
		chat:     chat,
		model:    model,
		provider: c.provider,
		config:   config,
	}, nil
}

// Chat is a tracing wrapper around *genai.Chat.
type Chat struct {
	chat     *genai.Chat
	model    string
	provider string
	config   *genai.GenerateContentConfig
}

// Raw returns the underlying *genai.Chat.
func (c *Chat) Raw() *genai.Chat {
	if c == nil {
		return nil
	}
	return c.chat
}

// History delegates to the underlying *genai.Chat.
func (c *Chat) History(curated bool) []*genai.Content {
	return c.chat.History(curated)
}

// SendMessage sends a message to the chat and emits an LLM span. The input
// captured on the span is a pre-call snapshot of the chat history plus the
// new user message (genai.Chat mutates History during a successful call).
func (c *Chat) SendMessage(ctx context.Context, parts ...genai.Part) (*genai.GenerateContentResponse, error) {
	if c == nil || c.chat == nil {
		return nil, errNilClient
	}
	input := c.snapshotInput(parts)
	span, ctx := startLLMSpan(ctx, "genai.chat.send_message", c.model, c.provider)

	resp, err := c.chat.SendMessage(ctx, parts...)
	finishLLMSpan(span, input, c.config, resp, err)
	return resp, err
}

// SendMessageStream sends a message and streams the response, emitting an
// LLM span covering the stream.
func (c *Chat) SendMessageStream(ctx context.Context, parts ...genai.Part) iter.Seq2[*genai.GenerateContentResponse, error] {
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		if c == nil || c.chat == nil {
			yield(nil, errNilClient)
			return
		}
		input := c.snapshotInput(parts)
		span, ctx := startLLMSpan(ctx, "genai.chat.send_message_stream", c.model, c.provider)

		acc := newStreamAccumulator()
		var lastErr error
		for chunk, err := range c.chat.SendMessageStream(ctx, parts...) {
			if err != nil {
				lastErr = err
			} else {
				acc.add(chunk)
			}
			if !yield(chunk, err) {
				break
			}
		}
		finishLLMSpan(span, input, c.config, acc.response(), lastErr)
	}
}

// snapshotInput returns the history at call time plus the new user message,
// taken before genai.Chat mutates History with the turn being sent.
func (c *Chat) snapshotInput(parts []genai.Part) []*genai.Content {
	src := c.chat.History(false)
	hist := make([]*genai.Content, len(src))
	copy(hist, src)
	userParts := make([]*genai.Part, 0, len(parts))
	for i := range parts {
		p := parts[i]
		userParts = append(userParts, &p)
	}
	return append(hist, &genai.Content{Parts: userParts, Role: string(genai.RoleUser)})
}
