// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"math/big"
	"strconv"
)

// DistributedContext is the LLMObs context that one process sends to the next.
type DistributedContext struct {
	ParentID          string
	TraceID           string
	MLApp             string
	SessionID         string
	ParentAgentSpanID string
	// ParentAgentName is empty when ParentAgentSpanID is empty.
	ParentAgentName string
}

// DistributedContext returns the context that a downstream service inherits from s.
// It holds the read lock because Annotate can change the session ID at any time.
func (s *Span) DistributedContext() DistributedContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dc := DistributedContext{
		ParentID:          s.SpanID(),
		TraceID:           s.llmTraceID,
		MLApp:             s.mlApp,
		SessionID:         s.sessionID,
		ParentAgentSpanID: s.PropagatedParentAgentSpanID(),
	}
	if dc.ParentAgentSpanID != "" {
		dc.ParentAgentName = s.PropagatedParentAgentName()
	}
	return dc
}

// DistributedContext returns p unchanged, for a service that forwards a request
// without starting an LLMObs span of its own.
func (p *PropagatedLLMSpan) DistributedContext() DistributedContext {
	dc := DistributedContext{
		ParentID:          p.SpanID,
		TraceID:           p.TraceID,
		MLApp:             p.MLApp,
		SessionID:         p.SessionID,
		ParentAgentSpanID: p.ParentAgentSpanID,
	}
	if dc.ParentAgentSpanID != "" {
		dc.ParentAgentName = p.ParentAgentName
	}
	return dc
}

// WireTraceID returns the LLMObs trace ID for the _dd.p.llmobs_trace_id tag: the
// form the tag arrived in, or the canonical form for a trace that started here.
func (s *Span) WireTraceID() string {
	if s.wireTraceID != "" {
		return s.wireTraceID
	}
	return s.llmTraceID
}

// NormalizeTraceID converts an LLMObs trace ID read from the _dd.p.llmobs_trace_id
// tag to the canonical form: 32-char lowercase hex for a 128-bit ID, decimal for
// an ID that fits in 64 bits. It matches dd-trace-py's _normalize_wire_trace_id_to_hex.
//
// dd-trace-py writes the tag in decimal, and dd-trace-go writes it in hex. A
// 32-char value with a-f or a leading zero can only be hex. A 32-char value of
// digits only is read as decimal, so about 1 in 3.78 million hex IDs is misread.
//
// A value that is neither decimal nor 32-char hex is returned unchanged.
func NormalizeTraceID(v string) string {
	if isHexTraceID(v) && (v[0] == '0' || !isDigits(v)) {
		return v
	}
	if !isDigits(v) {
		return v
	}
	n, ok := new(big.Int).SetString(v, 10)
	if !ok || n.BitLen() > 128 {
		return v
	}
	if n.IsUint64() {
		return strconv.FormatUint(n.Uint64(), 10)
	}
	return padHex(n.Text(16))
}

// TraceIDToWire converts a canonical LLMObs trace ID to the decimal form that
// dd-trace-py writes to the _dd.p.llmobs_trace_id tag. Any other value is
// returned unchanged.
func TraceIDToWire(v string) string {
	if !isHexTraceID(v) {
		return v
	}
	n, ok := new(big.Int).SetString(v, 16)
	if !ok {
		return v
	}
	return n.Text(10)
}

// IsCanonicalTraceID reports whether v is in the canonical form that the
// llmobs.trace_id baggage item must carry: 32 lowercase hex characters, or a
// decimal number from 1 to 2^64-1 without leading zeros. This is what
// dd-trace-py's format_trace_id produces.
func IsCanonicalTraceID(v string) bool {
	if isHexTraceID(v) {
		return true
	}
	if v == "" || v[0] == '0' || !isDigits(v) {
		return false
	}
	_, err := strconv.ParseUint(v, 10, 64)
	return err == nil
}

func isHexTraceID(v string) bool {
	if len(v) != 32 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func isDigits(v string) bool {
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}

func padHex(h string) string {
	const zeros = "00000000000000000000000000000000"
	if len(h) >= 32 {
		return h
	}
	return zeros[:32-len(h)] + h
}
