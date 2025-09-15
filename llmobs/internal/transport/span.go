// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package transport

import (
	"context"
	"fmt"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type LLMObsSpanEvent struct {
	SpanID           string            `json:"span_id,omitempty"`
	TraceID          string            `json:"trace_id,omitempty"`
	ParentID         string            `json:"parent_id,omitempty"`
	SessionID        string            `json:"session_id,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Service          string            `json:"service,omitempty"`
	Name             string            `json:"name,omitempty"`
	StartNS          int64             `json:"start_ns,omitempty"`
	Duration         int64             `json:"duration,omitempty"`
	Status           string            `json:"status,omitempty"`
	StatusMessage    string            `json:"status_message,omitempty"`
	Meta             map[string]any    `json:"meta,omitempty"`
	Metrics          map[string]any    `json:"metrics,omitempty"`
	CollectionErrors []string          `json:"collection_errors,omitempty"`
	SpanLinks        []tracer.SpanLink `json:"span_links,omitempty"`

	// _dd: Dict[str, str]
}

func (c *Client) LLMObsSpanSendEvents(
	ctx context.Context,
	events []*LLMObsSpanEvent,
) error {
	if len(events) == 0 {
		return nil
	}
	path := endpointLLMSpan
	method := http.MethodPost

	status, b, err := c.request(ctx, method, path, subdomainLLMSpan, events)
	if err != nil {
		return fmt.Errorf("post llmobs spans failed: %w", err)
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return fmt.Errorf("unexpected status %d: %s", status, string(b))
	}
	return nil
}
