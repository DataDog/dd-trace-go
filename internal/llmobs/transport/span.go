// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package transport

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// SpanKind identifies the kind of an LLM Obs span.
type SpanKind string

const (
	SpanKindExperiment SpanKind = "experiment"
	SpanKindWorkflow   SpanKind = "workflow"
	SpanKindLLM        SpanKind = "llm"
	SpanKindEmbedding  SpanKind = "embedding"
	SpanKindAgent      SpanKind = "agent"
	SpanKindRetrieval  SpanKind = "retrieval"
	SpanKindTask       SpanKind = "task"
	SpanKindTool       SpanKind = "tool"
)

// SpanLink links a span to another span. Its trace/span IDs are opaque decimal
// strings: the live tracer formats its uint64 IDs as decimal strings and the
// offline export path passes caller-assigned string IDs through verbatim.
type SpanLink struct {
	TraceID     string            `json:"trace_id"`
	TraceIDHigh string            `json:"trace_id_high,omitempty"`
	SpanID      string            `json:"span_id"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Tracestate  string            `json:"tracestate,omitempty"`
	Flags       uint32            `json:"flags,omitempty"`
}

type DDAttributes struct {
	SpanID     string `json:"span_id"`
	TraceID    string `json:"trace_id"`
	APMTraceID string `json:"apm_trace_id,omitempty"`
	Scope      string `json:"scope,omitempty"`
}

type LLMObsSpanEvent struct {
	// The fields without JSON tags are construction inputs for callers that build
	// completed spans offline. BuildExportSpan lowers them into the existing wire
	// fields below before submission.
	Kind          SpanKind       `json:"-"`
	ModelName     string         `json:"-"`
	ModelProvider string         `json:"-"`
	Input         string         `json:"-"`
	Output        string         `json:"-"`
	Metadata      map[string]any `json:"-"`
	Start         time.Time      `json:"-"`
	APMTraceID    string         `json:"-"`
	ErrorMessage  string         `json:"-"`
	ErrorType     string         `json:"-"`
	ErrorStack    string         `json:"-"`

	SpanID           string             `json:"span_id,omitempty"`
	TraceID          string             `json:"trace_id,omitempty"`
	ParentID         string             `json:"parent_id,omitempty"`
	SessionID        string             `json:"session_id,omitempty"`
	Tags             []string           `json:"tags,omitempty"`
	Name             string             `json:"name,omitempty"`
	Service          string             `json:"service,omitempty"`
	StartNS          int64              `json:"start_ns,omitempty"`
	Duration         int64              `json:"duration,omitempty"`
	Status           string             `json:"status,omitempty"`
	StatusMessage    string             `json:"status_message,omitempty"`
	Meta             map[string]any     `json:"meta,omitempty"`
	Metrics          map[string]float64 `json:"metrics,omitempty"`
	CollectionErrors []string           `json:"collection_errors,omitempty"`
	SpanLinks        []SpanLink         `json:"span_links,omitempty"`
	DDAttributes     DDAttributes       `json:"_dd"`
}

type PushSpanEventsRequest struct {
	Stage         string             `json:"_dd.stage,omitempty"`
	TracerVersion string             `json:"_dd.tracer_version,omitempty"`
	Scope         string             `json:"_dd.scope,omitempty"`
	EventType     string             `json:"event_type,omitempty"`
	Spans         []*LLMObsSpanEvent `json:"spans,omitempty"`
}

// NewPushSpanEventsRequests builds the /api/v2/llmobs request envelopes for
// events: one envelope per span, because _dd.scope is a per-envelope field taken
// from the span's own _dd.scope, so spans with differing scopes cannot share one.
// Shared by the live flush and the offline export client so both emit the same
// envelope shape.
func NewPushSpanEventsRequests(events []*LLMObsSpanEvent) []*PushSpanEventsRequest {
	body := make([]*PushSpanEventsRequest, 0, len(events))
	for _, ev := range events {
		req := &PushSpanEventsRequest{
			Stage:         "raw",
			TracerVersion: version.Tag,
			EventType:     "span",
			Spans:         []*LLMObsSpanEvent{ev},
		}
		if ev.DDAttributes.Scope != "" {
			req.Scope = ev.DDAttributes.Scope
		}
		body = append(body, req)
	}
	return body
}

func (c *Transport) PushSpanEvents(
	ctx context.Context,
	events []*LLMObsSpanEvent,
) error {
	_, err := c.PushSpanEventsWithResult(ctx, events)
	return err
}

// PushSpanEventsWithResult sends span events and returns request details.
func (c *Transport) PushSpanEventsWithResult(
	ctx context.Context,
	events []*LLMObsSpanEvent,
) (RequestResult, error) {
	if len(events) == 0 {
		return RequestResult{}, nil
	}
	path := EndpointLLMSpan
	method := http.MethodPost
	body := NewPushSpanEventsRequests(events)

	result, err := c.jsonRequest(ctx, method, path, SubdomainLLMSpan, body, defaultTimeout)
	if err != nil {
		return summarizeRequest(result), err
	}
	if result.statusCode != http.StatusOK && result.statusCode != http.StatusAccepted {
		return summarizeRequest(result), fmt.Errorf("unexpected status %d: %s", result.statusCode, string(result.body))
	}
	return summarizeRequest(result), nil
}
