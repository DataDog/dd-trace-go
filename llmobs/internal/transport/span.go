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
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

type LLMObsSpanEvent struct {
	SpanID           string            `json:"span_id,omitempty"`
	TraceID          string            `json:"trace_id,omitempty"`
	ParentID         string            `json:"parent_id,omitempty"`
	SessionID        string            `json:"session_id,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Name             string            `json:"name,omitempty"`
	StartNS          int64             `json:"start_ns,omitempty"`
	Duration         int64             `json:"duration,omitempty"`
	Status           string            `json:"status,omitempty"`
	StatusMessage    string            `json:"status_message,omitempty"`
	Meta             map[string]any    `json:"meta,omitempty"`
	Metrics          map[string]any    `json:"metrics,omitempty"`
	CollectionErrors []string          `json:"collection_errors,omitempty"`
	SpanLinks        []tracer.SpanLink `json:"span_links,omitempty"`
	Scope            string            `json:"-"`
}

type RequestLLMObsSpanEventsCreate struct {
	Stage         string             `json:"_dd.stage,omitempty"`
	TracerVersion string             `json:"_dd.tracer_version,omitempty"`
	Scope         string             `json:"_dd.scope,omitempty"`
	EventType     string             `json:"event_type,omitempty"`
	Spans         []*LLMObsSpanEvent `json:"spans,omitempty"`
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
	body := make([]*RequestLLMObsSpanEventsCreate, 0, len(events))
	for _, ev := range events {
		req := &RequestLLMObsSpanEventsCreate{
			Stage:         "raw",
			TracerVersion: version.Tag,
			EventType:     "span",
			Spans:         []*LLMObsSpanEvent{ev},
		}
		if ev.Scope != "" {
			req.Scope = ev.Scope
		}
		body = append(body, req)
	}

	status, b, err := c.request(ctx, method, path, subdomainLLMSpan, body)
	if err != nil {
		return fmt.Errorf("post llmobs spans failed: %w", err)
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return fmt.Errorf("unexpected status %d: %s", status, string(b))
	}
	return nil
}
