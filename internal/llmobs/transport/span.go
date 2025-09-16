// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package transport

import (
	"context"
	"fmt"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

type SpanLink struct {
	// TraceID represents the low 64 bits of the linked span's trace id. This field is required.
	TraceID uint64 `msg:"trace_id" json:"trace_id"`
	// TraceIDHigh represents the high 64 bits of the linked span's trace id. This field is only set if the linked span's trace id is 128 bits.
	TraceIDHigh uint64 `msg:"trace_id_high,omitempty" json:"trace_id_high"`
	// SpanID represents the linked span's span id.
	SpanID uint64 `msg:"span_id" json:"span_id"`
	// Attributes is a mapping of keys to string values. These values are used to add additional context to the span link.
	Attributes map[string]string `msg:"attributes,omitempty" json:"attributes"`
	// Tracestate is the tracestate of the linked span. This field is optional.
	Tracestate string `msg:"tracestate,omitempty" json:"tracestate"`
	// Flags represents the W3C trace flags of the linked span. This field is optional.
	Flags uint32 `msg:"flags,omitempty" json:"flags"`
}

type LLMObsSpanEvent struct {
	SpanID           string         `json:"span_id,omitempty"`
	TraceID          string         `json:"trace_id,omitempty"`
	ParentID         string         `json:"parent_id,omitempty"`
	SessionID        string         `json:"session_id,omitempty"`
	Tags             []string       `json:"tags,omitempty"`
	Name             string         `json:"name,omitempty"`
	StartNS          int64          `json:"start_ns,omitempty"`
	Duration         int64          `json:"duration,omitempty"`
	Status           string         `json:"status,omitempty"`
	StatusMessage    string         `json:"status_message,omitempty"`
	Meta             map[string]any `json:"meta,omitempty"`
	Metrics          map[string]any `json:"metrics,omitempty"`
	CollectionErrors []string       `json:"collection_errors,omitempty"`
	SpanLinks        []SpanLink     `json:"span_links,omitempty"`
	Scope            string         `json:"-"`
}

type RequestLLMObsSpanEventsCreate struct {
	Stage         string             `json:"_dd.stage,omitempty"`
	TracerVersion string             `json:"_dd.tracer_version,omitempty"`
	Scope         string             `json:"_dd.scope,omitempty"`
	EventType     string             `json:"event_type,omitempty"`
	Spans         []*LLMObsSpanEvent `json:"spans,omitempty"`
}

func (c *Transport) LLMObsSpanSendEvents(
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
