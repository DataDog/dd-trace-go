// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"strconv"
	"strings"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
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
// error type for telemetry. spanCtx must be non-nil.
func propagatedLLMSpan(spanCtx *SpanContext) (p *illmobs.PropagatedLLMSpan, carrier, errType string) {
	tag := func(k string) string {
		if spanCtx.trace == nil {
			return ""
		}
		return spanCtx.trace.propagatingTag(k)
	}
	tagParent := tag(keyPropagatedLLMObsParentID)
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
		return tag(tagKey)
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
		tagTraceID = tag(keyPropagatedLLMObsTraceID)
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

// activateInboundLLMObsContext reads the inbound LLMObs context once, at the local
// root of an inbound request, and stores it on the span context. Injections from
// the root itself or from any child then carry it, even when no context.Context
// passes it on. This is also where dd-trace-py counts the activation telemetry.
func activateInboundLLMObsContext(spanCtx *SpanContext) {
	p, carrier, errType := propagatedLLMSpan(spanCtx)
	illmobs.TrackDistributedContextCarrier(carrier, errType)
	if p != nil {
		spanCtx.setLLMObsPropagated(p, true)
	}
}

// traceLLMObs is the LLMObs context of one local trace.
type traceLLMObs struct {
	mu locking.RWMutex
	// propagated is the inbound context, which requests outside any LLMObs span carry.
	// +checklocks:mu
	propagated *illmobs.PropagatedLLMSpan
	// spans maps an APM span ID to the LLMObs span that requests injected from that
	// APM span carry: the LLMObs span active when it started, or its parent's.
	// +checklocks:mu
	spans map[uint64]*illmobs.Span
}

func (t *trace) llmobsState() *traceLLMObs {
	if s := t.llmobs.Load(); s != nil {
		return s
	}
	t.llmobs.CompareAndSwap(nil, &traceLLMObs{})
	return t.llmobs.Load()
}

// forgetLLMObsSpans drops the map entries of spans that a partial flush sent, so
// the map stays bounded like the trace's span buffer on a long-running trace.
// Unflushed spans keep their entry, so their contexts still propagate after
// Finish. Called with t.mu held; traceLLMObs.mu is never held while taking t.mu.
func (t *trace) forgetLLMObsSpans(flushed []*Span) {
	s := t.llmobs.Load()
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, span := range flushed {
		delete(s.spans, span.context.SpanID())
	}
}

// llmobsContext returns the LLMObs span, or else the inbound context, that
// requests injected from c carry.
func (c *SpanContext) llmobsContext() (*illmobs.Span, *illmobs.PropagatedLLMSpan) {
	if c.trace == nil {
		return nil, nil
	}
	s := c.trace.llmobs.Load()
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.spans[c.spanID], s.propagated
}

func (c *SpanContext) setLLMObsSpan(span *illmobs.Span) {
	if c.trace == nil {
		return
	}
	s := c.trace.llmobsState()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spans == nil {
		s.spans = make(map[uint64]*illmobs.Span)
	}
	s.spans[c.spanID] = span
}

// setLLMObsPropagated records the inbound context. The local root sets it with
// replace; a span that only finds it in its context.Context fills it in if unset.
func (c *SpanContext) setLLMObsPropagated(p *illmobs.PropagatedLLMSpan, replace bool) {
	if c.trace == nil {
		return
	}
	s := c.trace.llmobsState()
	s.mu.Lock()
	defer s.mu.Unlock()
	if replace || s.propagated == nil {
		s.propagated = p
	}
}

// inheritLLMObsSpan makes a child span carry its parent's LLMObs span, for
// children started without a context.Context.
func inheritLLMObsSpan(child, parent *SpanContext) {
	if span, _ := parent.llmobsContext(); span != nil {
		child.setLLMObsSpan(span)
	}
}

// setLLMObsTagsForInjection writes the _dd.p.llmobs_* tags for one request from the
// same per-span context as the llmobs.* baggage items, right before the tag
// carriers are encoded, so both carriers name the same parent. Without an LLMObs
// context it clears them, as dd-trace-py sends no parent then. The tags are
// trace-shared, so two concurrent injections can still race between this write
// and the encoding.
func setLLMObsTagsForInjection(ctx *SpanContext) {
	if ctx.trace == nil || !illmobs.Enabled() {
		return
	}
	t := ctx.trace
	dc, _ := ctx.llmobsDistributedContext() // zero value without an LLMObs context
	// The identity tags are validated at read: a numeric parent and a normalized trace ID.
	for _, kv := range [...][2]string{
		{keyPropagatedLLMObsParentID, dc.ParentID},
		{keyPropagatedLLMObsTraceID, dc.WireTraceID},
	} {
		if kv[1] == "" {
			t.unsetPropagatingTag(kv[0])
		} else {
			t.setPropagatingTag(kv[0], kv[1])
		}
	}
	// The other values can come from an inbound baggage header, which nothing caps.
	// One invalid or oversized value would make the encoder drop the whole
	// x-datadog-tags header, so it is left out of the tags; baggage still carries it.
	// A previous injection's values are cleared first, so they don't count against
	// this request's budget. The agent name has the lowest priority and goes last.
	optional := [...][2]string{
		{keyPropagatedLLMObsMLAPP, dc.MLApp},
		{keyPropagatedLLMObsSessionID, dc.SessionID},
		{keyPropagatedLLMObsSampleRate, dc.SampleRate},
		{keyPropagatedLLMObsSamplingDecision, dc.SamplingDecision},
	}
	for _, kv := range optional {
		t.unsetPropagatingTag(kv[0])
	}
	setPropagatingParentAgentTags(ctx, dc.ParentAgentSpanID, "")
	for _, kv := range optional {
		if kv[1] != "" && isValidPropagatableTag(kv[0], kv[1]) == nil && fitsPropagatingTagBudget(ctx, kv[0], kv[1]) {
			t.setPropagatingTag(kv[0], kv[1])
		}
	}
	setPropagatingParentAgentTags(ctx, dc.ParentAgentSpanID, dc.ParentAgentName)
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
	span, p := c.llmobsContext()
	switch {
	case span != nil:
		return span.DistributedContext(), true
	case p != nil:
		return p.DistributedContext(), true
	}
	// An extracted context that is injected without a local span (a relay) still
	// forwards what it received. It has no local root, so nothing decoded it yet.
	if (c.trace == nil || c.trace.root == nil) && illmobs.Enabled() { // +checklocksignore - root is write-once at init.
		if p, _, _ := propagatedLLMSpan(c); p != nil {
			return p.DistributedContext(), true
		}
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

// callerBaggageHeader returns the baggage header that the caller already set.
// Repeated HTTP header lines form one comma-separated list (RFC 9110). Other
// carriers keep lookupBaggageHeader's last-value rule.
func callerBaggageHeader(reader TextMapReader) (string, error) {
	var h HTTPHeadersCarrier
	switch c := reader.(type) {
	case HTTPHeadersCarrier:
		h = c
	case *HTTPHeadersCarrier:
		if c != nil {
			h = *c
		}
	default:
		return lookupBaggageHeader(reader)
	}
	var vals []string
	for k, vs := range h {
		if len(k) == baggageKeyLen && strings.EqualFold(k, "baggage") {
			vals = append(vals, vs...)
		}
	}
	return strings.Join(vals, ","), nil
}

// deleteBaggageHeader removes the baggage header from the built-in carriers.
// Other carriers cannot delete a key, so their header is left as it was.
func deleteBaggageHeader(writer TextMapWriter) { cleanBaggageKeys(writer, false) }

// cleanBaggageKeys deletes every spelling of the baggage key from the built-in
// carriers, by value or by pointer. With keepWritten, it keeps the key that the
// carrier's Set writes.
func cleanBaggageKeys(writer TextMapWriter, keepWritten bool) {
	keep := func(k string) string {
		if keepWritten {
			return k
		}
		return ""
	}
	switch c := writer.(type) {
	case HTTPHeadersCarrier:
		deleteBaggageKeys(c, keep("Baggage"))
	case *HTTPHeadersCarrier:
		if c != nil {
			deleteBaggageKeys(*c, keep("Baggage"))
		}
	case TextMapCarrier:
		deleteBaggageKeys(c, keep("baggage"))
	case *TextMapCarrier:
		if c != nil {
			deleteBaggageKeys(*c, keep("baggage"))
		}
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
