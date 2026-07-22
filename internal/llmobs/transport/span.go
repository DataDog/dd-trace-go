// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// SpanLinkID models a span-link trace/span identifier, which is a genuinely
// polymorphic wire field: it marshals as a JSON number for a numeric ID and as a
// JSON string for an opaque one, and unmarshals from either. A single Go type
// carries both because the two producers of this shared SpanLink wire struct
// require different representations that cannot be reconciled without regressing
// one of them:
//
//   - the live tracer emits its native uint64 IDs as JSON numbers — its historical
//     wire shape, which the LLM Obs intake has always received and which a prior
//     review round declined to change; and
//   - the offline export path (llmobs/export) is built on caller-assigned IDs, so
//     a link must reference another span by the same opaque, caller-owned string
//     ID that span was given, which is not necessarily numeric.
//
// Both share this struct (reused rather than duplicated, per reviewer request)
// and the intake accepts either form on this field. Modeling a number-or-string
// wire field with a custom Marshaler/Unmarshaler is the idiomatic Go approach;
// the NumericSpanLinkID / StringSpanLinkID constructors make each producer's
// choice explicit at the call site, so the polymorphism is confined to the wire,
// not smuggled through an untyped API.
type SpanLinkID struct {
	num   uint64
	str   string
	isStr bool
}

// NumericSpanLinkID returns a span-link ID that marshals as a JSON number, for
// the live tracer path.
func NumericSpanLinkID(n uint64) SpanLinkID { return SpanLinkID{num: n} }

// StringSpanLinkID returns a span-link ID that marshals as a JSON string, for
// the offline export path's opaque caller-owned IDs.
func StringSpanLinkID(s string) SpanLinkID { return SpanLinkID{str: s, isStr: true} }

// MarshalJSON implements json.Marshaler.
func (id SpanLinkID) MarshalJSON() ([]byte, error) {
	if id.isStr {
		return json.Marshal(id.str)
	}
	return json.Marshal(id.num)
}

// UnmarshalJSON implements json.Unmarshaler, accepting either a JSON number
// (numeric ID) or a JSON string (opaque ID). Without it, the shared SpanLink
// wire struct could no longer be decoded on paths that read it back (e.g. the
// in-process test collector), since a marshal-only type breaks json.Unmarshal.
func (id *SpanLinkID) UnmarshalJSON(b []byte) error {
	var n uint64
	if err := json.Unmarshal(b, &n); err == nil {
		*id = SpanLinkID{num: n}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*id = SpanLinkID{str: s, isStr: true}
	return nil
}

// SpanLink links a span to another span. Its trace/span IDs use SpanLinkID so a
// single wire struct supports both numeric IDs (the live tracer) and opaque
// string IDs (the offline export path).
type SpanLink struct {
	TraceID     SpanLinkID        `json:"trace_id"`
	TraceIDHigh *SpanLinkID       `json:"trace_id_high,omitempty"`
	SpanID      SpanLinkID        `json:"span_id"`
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

func (c *Transport) PushSpanEvents(
	ctx context.Context,
	events []*LLMObsSpanEvent,
) error {
	if len(events) == 0 {
		return nil
	}
	path := EndpointLLMSpan
	method := http.MethodPost
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

	result, err := c.jsonRequest(ctx, method, path, SubdomainLLMSpan, body, defaultTimeout)
	if err != nil {
		return err
	}
	if result.statusCode != http.StatusOK && result.statusCode != http.StatusAccepted {
		return fmt.Errorf("unexpected status %d: %s", result.statusCode, string(result.body))
	}
	return nil
}
