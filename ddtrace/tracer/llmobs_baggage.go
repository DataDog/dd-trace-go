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

// LLMObs distributed context travels in the W3C baggage header under the llmobs.*
// keys, next to the _dd.p.llmobs_* tags. Baggage survives an intermediary that
// drops the APM trace headers. The keys and rules are a cross-SDK contract, and
// dd-trace-py is the reference implementation.
const (
	baggageKeyLLMObsParentID          = "llmobs.parent_id"
	baggageKeyLLMObsTraceID           = "llmobs.trace_id" // canonical form, never the decimal tag form
	baggageKeyLLMObsMLApp             = "llmobs.ml_app"
	baggageKeyLLMObsSessionID         = "llmobs.session_id"
	baggageKeyLLMObsSampleRate        = "llmobs.sample_rate"
	baggageKeyLLMObsSamplingDecision  = "llmobs.sampling_decision"
	baggageKeyLLMObsParentAgentSpanID = "llmobs.parent_agent_span_id"
	baggageKeyLLMObsParentAgentName   = "llmobs.parent_agent_name"

	baggageLLMObsPrefix = "llmobs."

	// Limits on percent-encoded bytes, which the baggage byte cap counts. The agent
	// name is cut to its limit. Any other value over the limit is not sent: one
	// oversized item would stop the injector and drop every item after it.
	baggageLLMObsAgentNameMaxEncodedBytes = 128
	baggageLLMObsValueMaxEncodedBytes     = 512

	llmobsRootParentID = "undefined"

	// Values of the carrier and error_type tags of the activation telemetry.
	llmobsCarrierBaggage      = "baggage"
	llmobsCarrierTags         = "tags"
	llmobsCarrierStaleBaggage = "stale_baggage"
	llmobsCarrierNone         = "none"
	llmobsErrMissingParentID  = "missing_parent_id"
	llmobsErrInvalidParentID  = "invalid_parent_id"
	llmobsErrMissingTraceID   = "missing_parent_llmobs_trace_id"
)

// isLLMObsBaggageKey reports whether k is one of the llmobs.* keys this tracer
// owns. With LLMObs enabled, an inbound or caller-set copy of k is never
// forwarded: k is written from the request's own LLMObs context, or not at all.
func isLLMObsBaggageKey(k string) bool {
	switch k {
	case baggageKeyLLMObsParentID, baggageKeyLLMObsTraceID, baggageKeyLLMObsMLApp, baggageKeyLLMObsSessionID,
		baggageKeyLLMObsSampleRate, baggageKeyLLMObsSamplingDecision,
		baggageKeyLLMObsParentAgentSpanID, baggageKeyLLMObsParentAgentName:
		return true
	}
	return false
}

// propagatedLLMSpan reads the inbound LLMObs context from one carrier, as
// dd-trace-py's _select_propagated_carrier does. Baggage is preferred. A real tag
// parent that differs from the baggage parent means a tags-only hop forwarded
// stale baggage, so then the tags are used. It also returns the carrier and the
// error type for telemetry. spanCtx and spanCtx.trace must be non-nil.
func propagatedLLMSpan(spanCtx *SpanContext) (p *illmobs.PropagatedLLMSpan, carrier, errType string) {
	tagParent := spanCtx.trace.propagatingTag(keyPropagatedLLMObsParentID)
	baggageParent := spanCtx.baggageItem(baggageKeyLLMObsParentID)
	parent := baggageParent
	switch {
	case baggageParent == "" && tagParent == "":
		return nil, llmobsCarrierNone, llmobsErrMissingParentID
	case baggageParent == "":
		carrier, parent = llmobsCarrierTags, tagParent
	case tagParent != "" && tagParent != llmobsRootParentID && tagParent != baggageParent:
		carrier, parent = llmobsCarrierStaleBaggage, tagParent
	default:
		carrier = llmobsCarrierBaggage
	}
	if parent == llmobsRootParentID {
		return nil, carrier, llmobsErrMissingParentID
	}
	if _, err := strconv.ParseUint(parent, 10, 64); err != nil {
		return nil, carrier, llmobsErrInvalidParentID
	}

	fromBaggage := carrier == llmobsCarrierBaggage
	// One hop wrote both carriers, so a field missing from baggage can come from the tags.
	tagsMatch := fromBaggage && tagParent == baggageParent
	read := func(baggageKey, tagKey string) string {
		if fromBaggage {
			if v := spanCtx.baggageItem(baggageKey); v != "" || !tagsMatch {
				return v
			}
		}
		return spanCtx.trace.propagatingTag(tagKey)
	}
	p = &illmobs.PropagatedLLMSpan{
		SpanID:            parent,
		MLApp:             read(baggageKeyLLMObsMLApp, keyPropagatedLLMObsMLAPP),
		SessionID:         read(baggageKeyLLMObsSessionID, keyPropagatedLLMObsSessionID),
		SampleRate:        read(baggageKeyLLMObsSampleRate, keyPropagatedLLMObsSampleRate),
		SamplingDecision:  read(baggageKeyLLMObsSamplingDecision, keyPropagatedLLMObsSamplingDecision),
		ParentAgentSpanID: read(baggageKeyLLMObsParentAgentSpanID, keyPropagatedLLMObsParentAgentSpanID),
	}
	if p.ParentAgentSpanID != "" {
		p.ParentAgentName = read(baggageKeyLLMObsParentAgentName, keyPropagatedLLMObsParentAgentName)
	}

	var baggageTraceID, tagTraceID string
	if fromBaggage {
		baggageTraceID = spanCtx.baggageItem(baggageKeyLLMObsTraceID)
	}
	if !fromBaggage || tagsMatch {
		tagTraceID = spanCtx.trace.propagatingTag(keyPropagatedLLMObsTraceID)
	}
	switch {
	case illmobs.IsCanonicalTraceID(baggageTraceID):
		p.TraceID = baggageTraceID
	case tagTraceID != "":
		p.TraceID = illmobs.NormalizeTraceID(tagTraceID)
	default:
		// As in dd-trace-py: the caller's APM trace ID, or a new one.
		p.TraceID, errType = apmTraceID(spanCtx), llmobsErrMissingTraceID
	}
	// The tag is sent on in the form its writer chose; dd-trace-py writes decimal.
	p.WireTraceID = tagTraceID
	if p.WireTraceID == "" {
		p.WireTraceID = illmobs.TraceIDToWire(p.TraceID)
	}
	return p, carrier, errType
}

// apmTraceID returns the APM trace ID of spanCtx in canonical form.
func apmTraceID(spanCtx *SpanContext) string {
	if spanCtx.TraceIDUpper() == 0 {
		return strconv.FormatUint(spanCtx.TraceIDLower(), 10)
	}
	return spanCtx.TraceID()
}

// llmobsDistributedContext returns the LLMObs context that a request injected
// from c carries. It is computed at injection and never stored in c's baggage,
// which every child span copies.
func (c *SpanContext) llmobsDistributedContext() (illmobs.DistributedContext, bool) {
	switch {
	case c.llmobsSpan != nil:
		return c.llmobsSpan.DistributedContext(), true
	case c.llmobsPropagated != nil:
		return c.llmobsPropagated.DistributedContext(), true
	}
	return illmobs.DistributedContext{}, false
}

// llmobsBaggageItems returns the llmobs.* items for dc, in dd-trace-py's order.
// An empty value is not sent.
func llmobsBaggageItems(dc illmobs.DistributedContext) [8][2]string {
	return [8][2]string{
		{baggageKeyLLMObsParentID, llmobsBaggageValue(dc.ParentID)},
		{baggageKeyLLMObsTraceID, llmobsBaggageValue(dc.TraceID)},
		{baggageKeyLLMObsMLApp, llmobsBaggageValue(dc.MLApp)},
		{baggageKeyLLMObsSessionID, llmobsBaggageValue(dc.SessionID)},
		{baggageKeyLLMObsSampleRate, llmobsBaggageValue(dc.SampleRate)},
		{baggageKeyLLMObsSamplingDecision, llmobsBaggageValue(dc.SamplingDecision)},
		{baggageKeyLLMObsParentAgentSpanID, llmobsBaggageValue(dc.ParentAgentSpanID)},
		{baggageKeyLLMObsParentAgentName, llmobsBaggageValue(
			truncateToEncodedBytes(dc.ParentAgentName, baggageLLMObsAgentNameMaxEncodedBytes))},
	}
}

// llmobsBaggageValue returns v, or "" when v must not be sent. A blank value
// encodes to an empty member, which makes the receiver reject the whole header.
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
		deleteBaggageKeys(c, "")
	case TextMapCarrier:
		deleteBaggageKeys(c, "")
	}
}

// deleteBaggageKeys deletes every key of m that is "baggage" in any case, except keep.
func deleteBaggageKeys[M ~map[string]V, V any](m M, keep string) {
	for k := range m {
		if k != keep && len(k) == baggageKeyLen && strings.EqualFold(k, "baggage") {
			delete(m, k)
		}
	}
}

// deleteLLMObsBaggage removes every llmobs.* item from a freshly extracted
// context, for the restart extract behavior: the LLMObs trace must not continue
// across that boundary either.
// +checklocksignore — Initialization time, freshly extracted ctx not yet shared.
func deleteLLMObsBaggage(ctx *SpanContext) {
	for k := range ctx.baggage {
		if strings.HasPrefix(k, baggageLLMObsPrefix) {
			delete(ctx.baggage, k)
		}
	}
}
