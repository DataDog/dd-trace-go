// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package genaitrace

import (
	"google.golang.org/genai"
)

// Client is a tracing wrapper around *genai.Client. Calls on its Models and
// Chats fields produce LLM Observability spans. Use Raw to access the
// underlying *genai.Client for services that are not (yet) instrumented.
type Client struct {
	raw      *genai.Client
	provider string

	// Models wraps the underlying *genai.Client.Models with tracing.
	Models *Models
	// Chats wraps the underlying *genai.Client.Chats with tracing.
	Chats *Chats
}

// WrapClient returns a tracing wrapper around the given *genai.Client.
// The returned *Client exposes wrapper Models and Chats with the same
// method signatures as the upstream SDK.
//
// Options are reserved for future integration-level configuration; none
// are currently exported, but the variadic form is kept so we can add
// them without a breaking API change.
func WrapClient(c *genai.Client, opts ...Option) *Client {
	if c == nil {
		return nil
	}
	cfg := defaults()
	for _, opt := range opts {
		opt(cfg)
	}
	provider := backendProvider(c)
	tc := &Client{
		raw:      c,
		provider: provider,
	}
	tc.Models = &Models{m: c.Models, provider: provider}
	tc.Chats = &Chats{c: c.Chats, provider: provider}
	return tc
}

// Raw returns the underlying *genai.Client.
func (c *Client) Raw() *genai.Client {
	if c == nil {
		return nil
	}
	return c.raw
}

func backendProvider(c *genai.Client) string {
	switch c.ClientConfig().Backend {
	case genai.BackendVertexAI:
		return "google_vertexai"
	case genai.BackendEnterprise:
		return "google_enterprise"
	default:
		return "google"
	}
}
