// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"strconv"
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

	// Limits on percent-encoded bytes, which is what the baggage byte cap counts.
	// The agent name is cut to its limit. Any other value over its limit is not
	// sent, because one oversized item stops the injector and drops every item
	// after it. These match dd-trace-py.
	baggageLLMObsAgentNameMaxEncodedBytes = 128
	baggageLLMObsValueMaxEncodedBytes     = 512

	// The sampling keys are written by dd-trace-py, not by this tracer. They are
	// request-scoped like the others, so a caller's stale copy is not merged.
	baggageKeyLLMObsSampleRate       = "llmobs.sample_rate"
	baggageKeyLLMObsSamplingDecision = "llmobs.sampling_decision"

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

// isLLMObsRequestScopedKey reports whether k belongs to one request's LLMObs
// context, so that a copy left in a caller's baggage header is not merged.
func isLLMObsRequestScopedKey(k string) bool {
	return isLLMObsBaggageKeyWritten(k) || k == baggageKeyLLMObsSampleRate || k == baggageKeyLLMObsSamplingDecision
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
		return propagatedLLMSpanFromTags(spanCtx, tagParent), llmobsCarrierTags
	case tagParent != "" && tagParent != baggageParent:
		return propagatedLLMSpanFromTags(spanCtx, tagParent), llmobsCarrierStaleBaggage
	default:
		// When both carriers name the same parent, one hop wrote them, so a field
		// missing from baggage can come from the tags.
		return propagatedLLMSpanFromBaggage(spanCtx, baggageParent, tagParent == baggageParent), llmobsCarrierBaggage
	}
}

// apmTraceID returns the APM trace ID of spanCtx in the canonical form. It is
// the LLMObs trace ID of last resort, as in dd-trace-py: on a continued trace
// it is the caller's APM trace ID, and otherwise a new one.
func apmTraceID(spanCtx *SpanContext) string {
	if spanCtx.TraceIDUpper() == 0 {
		return strconv.FormatUint(spanCtx.TraceIDLower(), 10)
	}
	return spanCtx.TraceID()
}

func llmobsParentID(v string) string {
	if v == llmobsRootParentID {
		return ""
	}
	return v
}

func propagatedLLMSpanFromTags(spanCtx *SpanContext, parentID string) *illmobs.PropagatedLLMSpan {
	t := spanCtx.trace
	p := &illmobs.PropagatedLLMSpan{
		SpanID:    parentID,
		MLApp:     t.propagatingTag(keyPropagatedLLMObsMLAPP),
		SessionID: t.propagatingTag(keyPropagatedLLMObsSessionID),
	}
	if traceID := t.propagatingTag(keyPropagatedLLMObsTraceID); traceID != "" {
		p.TraceID = illmobs.NormalizeTraceID(traceID)
		p.WireTraceID = traceID
	} else {
		p.TraceID = apmTraceID(spanCtx)
	}
	if agentSpanID := t.propagatingTag(keyPropagatedLLMObsParentAgentSpanID); agentSpanID != "" {
		p.ParentAgentSpanID = agentSpanID
		p.ParentAgentName = t.propagatingTag(keyPropagatedLLMObsParentAgentName)
	}
	return p
}

func propagatedLLMSpanFromBaggage(spanCtx *SpanContext, parentID string, tagsMatch bool) *illmobs.PropagatedLLMSpan {
	read := func(baggageKey, tagKey string) string {
		if v := spanCtx.baggageItem(baggageKey); v != "" {
			return v
		}
		if tagsMatch {
			return spanCtx.trace.propagatingTag(tagKey)
		}
		return ""
	}
	p := &illmobs.PropagatedLLMSpan{
		SpanID:    parentID,
		MLApp:     read(baggageKeyLLMObsMLApp, keyPropagatedLLMObsMLAPP),
		SessionID: read(baggageKeyLLMObsSessionID, keyPropagatedLLMObsSessionID),
	}
	tagTraceID := ""
	if tagsMatch {
		// The same hop wrote both carriers, so its tag form is sent on.
		tagTraceID = spanCtx.trace.propagatingTag(keyPropagatedLLMObsTraceID)
		p.WireTraceID = tagTraceID
	}
	switch traceID := spanCtx.baggageItem(baggageKeyLLMObsTraceID); {
	case illmobs.IsCanonicalTraceID(traceID):
		p.TraceID = traceID
	case tagTraceID != "":
		p.TraceID = illmobs.NormalizeTraceID(tagTraceID)
	default:
		p.TraceID = apmTraceID(spanCtx)
	}
	if agentSpanID := read(baggageKeyLLMObsParentAgentSpanID, keyPropagatedLLMObsParentAgentSpanID); agentSpanID != "" {
		p.ParentAgentSpanID = agentSpanID
		p.ParentAgentName = read(baggageKeyLLMObsParentAgentName, keyPropagatedLLMObsParentAgentName)
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
		{baggageKeyLLMObsParentID, llmobsBaggageValue(dc.ParentID)},
		{baggageKeyLLMObsTraceID, llmobsBaggageValue(dc.TraceID)},
		{baggageKeyLLMObsMLApp, llmobsBaggageValue(dc.MLApp)},
		{baggageKeyLLMObsSessionID, llmobsBaggageValue(dc.SessionID)},
		{baggageKeyLLMObsParentAgentSpanID, llmobsBaggageValue(dc.ParentAgentSpanID)},
		{baggageKeyLLMObsParentAgentName, llmobsBaggageValue(
			truncateToEncodedBytes(dc.ParentAgentName, baggageLLMObsAgentNameMaxEncodedBytes))},
	}
}

// llmobsBaggageValue returns v, or "" when v must not be sent. A blank value
// would encode to an empty member, which makes the receiving extractor reject
// the whole header. An oversized value would stop the injector.
func llmobsBaggageValue(v string) string {
	encoded := encodeValue(v)
	if encoded == "" || len(encoded) > baggageLLMObsValueMaxEncodedBytes {
		return ""
	}
	return v
}

// truncateToEncodedBytes cuts s at a rune boundary so that it percent-encodes
// to at most maxBytes.
func truncateToEncodedBytes(s string, maxBytes int) string {
	size := 0
	for i, r := range s {
		size += len(urlEncode(string(r), safeCharactersValue))
		if size > maxBytes {
			return s[:i]
		}
	}
	return s
}

// deleteBaggageHeader removes the baggage header from the built-in carriers.
// Other carriers cannot delete a key, so their header is left as it was.
func deleteBaggageHeader(writer TextMapWriter) {
	switch c := writer.(type) {
	case HTTPHeadersCarrier:
		for k := range c {
			if len(k) == baggageKeyLen && strings.EqualFold(k, "baggage") {
				delete(c, k)
			}
		}
	case TextMapCarrier:
		for k := range c {
			if len(k) == baggageKeyLen && strings.EqualFold(k, "baggage") {
				delete(c, k)
			}
		}
	}
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
