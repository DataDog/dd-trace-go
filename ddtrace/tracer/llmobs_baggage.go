// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"strings"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

// LLMObs distributed context also travels in the W3C baggage header, under the
// llmobs.* keys below. Baggage is extracted even when an intermediary drops the
// APM trace headers, so the LLMObs trace survives that hop. The keys and the
// rules in this file are a cross-SDK contract shared with dd-trace-py and
// dd-trace-js.
const (
	baggageKeyLLMObsParentID          = "llmobs.parent_id"
	baggageKeyLLMObsTraceID           = "llmobs.trace_id" // canonical form, never the decimal tag form
	baggageKeyLLMObsMLApp             = "llmobs.ml_app"
	baggageKeyLLMObsSessionID         = "llmobs.session_id"
	baggageKeyLLMObsParentAgentSpanID = "llmobs.parent_agent_span_id"
	baggageKeyLLMObsParentAgentName   = "llmobs.parent_agent_name"

	baggageLLMObsPrefix = "llmobs."

	// baggageLLMObsAgentNameMaxRunes caps the only free-form value, counted in
	// code points as dd-trace-py does.
	baggageLLMObsAgentNameMaxRunes = 512

	// llmobsRootParentID is the parent ID of a root LLMObs span. It never names a parent.
	llmobsRootParentID = "undefined"
)

// Values for the carrier tag of the activate_distributed_headers telemetry count.
const (
	llmobsCarrierBaggage      = "baggage"
	llmobsCarrierTags         = "tags"
	llmobsCarrierStaleBaggage = "stale_baggage"
)

// isLLMObsBaggageKeyWritten reports whether k is one of the keys this tracer
// writes. Inbound copies of these keys are replaced, never forwarded. Other
// llmobs.* keys (such as the sampling keys dd-trace-py writes) pass through.
func isLLMObsBaggageKeyWritten(k string) bool {
	switch k {
	case baggageKeyLLMObsParentID, baggageKeyLLMObsTraceID, baggageKeyLLMObsMLApp,
		baggageKeyLLMObsSessionID, baggageKeyLLMObsParentAgentSpanID, baggageKeyLLMObsParentAgentName:
		return true
	}
	return false
}

// propagatedLLMSpan reads the inbound LLMObs context from one carrier, and
// returns the carrier it used.
//
// Baggage is preferred. But when the tags name a different real parent, a hop
// that only writes the tags (an older tracer) sat between the caller and this
// service and forwarded the caller's baggage unchanged. Then the tags are the
// fresh copy. All fields come from the chosen carrier, so the context of two
// hops is never mixed. This matches dd-trace-py's _select_propagated_carrier.
//
// spanCtx and spanCtx.trace must be non-nil.
func propagatedLLMSpan(spanCtx *SpanContext) (*illmobs.PropagatedLLMSpan, string) {
	baggageParent := llmobsParentID(spanCtx.baggageItem(baggageKeyLLMObsParentID))
	tagParent := llmobsParentID(spanCtx.trace.propagatingTag(keyPropagatedLLMObsParentID))
	switch {
	case baggageParent == "" && tagParent == "":
		return nil, ""
	case baggageParent == "":
		return propagatedLLMSpanFromTags(spanCtx.trace, tagParent), llmobsCarrierTags
	case tagParent != "" && tagParent != baggageParent:
		return propagatedLLMSpanFromTags(spanCtx.trace, tagParent), llmobsCarrierStaleBaggage
	default:
		p := propagatedLLMSpanFromBaggage(spanCtx, baggageParent)
		if p != nil && tagParent != "" {
			// The same hop wrote both carriers, so its tag form is sent on.
			p.WireTraceID = spanCtx.trace.propagatingTag(keyPropagatedLLMObsTraceID)
		}
		return p, llmobsCarrierBaggage
	}
}

func llmobsParentID(v string) string {
	if v == llmobsRootParentID {
		return ""
	}
	return v
}

func propagatedLLMSpanFromTags(t *trace, parentID string) *illmobs.PropagatedLLMSpan {
	traceID := t.propagatingTag(keyPropagatedLLMObsTraceID)
	if traceID == "" {
		return nil
	}
	p := &illmobs.PropagatedLLMSpan{
		SpanID:      parentID,
		TraceID:     illmobs.NormalizeTraceID(traceID),
		WireTraceID: traceID,
		MLApp:       t.propagatingTag(keyPropagatedLLMObsMLAPP),
		SessionID:   t.propagatingTag(keyPropagatedLLMObsSessionID),
	}
	if agentSpanID := t.propagatingTag(keyPropagatedLLMObsParentAgentSpanID); agentSpanID != "" {
		p.ParentAgentSpanID = agentSpanID
		p.ParentAgentName = t.propagatingTag(keyPropagatedLLMObsParentAgentName)
	}
	return p
}

func propagatedLLMSpanFromBaggage(spanCtx *SpanContext, parentID string) *illmobs.PropagatedLLMSpan {
	traceID := spanCtx.baggageItem(baggageKeyLLMObsTraceID)
	if traceID == "" {
		return nil
	}
	p := &illmobs.PropagatedLLMSpan{
		SpanID:    parentID,
		TraceID:   traceID,
		MLApp:     spanCtx.baggageItem(baggageKeyLLMObsMLApp),
		SessionID: spanCtx.baggageItem(baggageKeyLLMObsSessionID),
	}
	if agentSpanID := spanCtx.baggageItem(baggageKeyLLMObsParentAgentSpanID); agentSpanID != "" {
		p.ParentAgentSpanID = agentSpanID
		p.ParentAgentName = spanCtx.baggageItem(baggageKeyLLMObsParentAgentName)
	}
	return p
}

// llmobsDistributedContext returns the LLMObs context that a request injected
// from c must carry. It is computed at injection, not stored in c's baggage:
// baggage is copied into every child span, which would cost every span start
// and let one request's parent stay behind for another.
func (c *SpanContext) llmobsDistributedContext() (illmobs.DistributedContext, bool) {
	switch {
	case c.llmobsSpan != nil:
		return c.llmobsSpan.DistributedContext(), true
	case c.llmobsPropagated != nil:
		return c.llmobsPropagated.DistributedContext(), true
	}
	return illmobs.DistributedContext{}, false
}

// llmobsBaggageItems returns the llmobs.* items for dc, in a fixed order. An
// empty value means the key is not sent.
func llmobsBaggageItems(dc illmobs.DistributedContext) [6][2]string {
	return [6][2]string{
		{baggageKeyLLMObsParentID, dc.ParentID},
		{baggageKeyLLMObsTraceID, dc.TraceID},
		{baggageKeyLLMObsMLApp, dc.MLApp},
		{baggageKeyLLMObsSessionID, dc.SessionID},
		{baggageKeyLLMObsParentAgentSpanID, dc.ParentAgentSpanID},
		{baggageKeyLLMObsParentAgentName, truncateRunes(dc.ParentAgentName, baggageLLMObsAgentNameMaxRunes)},
	}
}

func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// deleteLLMObsBaggage removes every llmobs.* item from a freshly extracted
// context. It is used for the restart extract behavior, which starts a new trace
// on purpose: the LLMObs trace must not continue across that boundary either.
// +checklocksignore — Initialization time, freshly extracted ctx not yet shared.
func deleteLLMObsBaggage(ctx *SpanContext) {
	for k := range ctx.baggage {
		if strings.HasPrefix(k, baggageLLMObsPrefix) {
			delete(ctx.baggage, k)
		}
	}
}
