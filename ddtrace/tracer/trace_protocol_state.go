// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

// traceProtocolState tracks what the tracer has learned about the Agent's
// support for /v1.0/traces, as a monotone lattice:
//
//	protoUnknown (0) < protoV1 (1) < protoV04 (2, terminal)
//
// The only mutator is (*config).advanceTraceProtocolState, and it never
// lowers the state. That is deliberate: both the /info-poll goroutine
// (refreshAgentFeatures) and a trace-send goroutine
// ((*agentTraceWriter).downgradeAfterRejectedSend) publish evidence into the
// same state with no lock between them, and monotonicity is what makes every
// possible interleaving converge on the same result instead of requiring one
// to be reasoned about.
//
// An earlier design gated re-upgrading to v1 on a streak of consecutive
// positive polls. That was rejected: /info polls and trace sends are
// independent requests that a load-balanced fleet can route to different
// backends, so no number of consecutive positive polls proves anything about
// where the next send lands. Once conclusive evidence says v0.4, it stays
// v0.4 for the life of the process — re-upgrading to v1 needs a restart. See
// doc.go for the resulting trade-off.
type traceProtocolState int32

const (
	// protoUnknown means no conclusive evidence yet: the Agent hasn't
	// answered /info successfully, or the only evidence so far was a
	// transient error. effectiveTraceProtocol resolves this to v0.4 (the
	// safe default), but unlike protoV04 the state can still advance either
	// way.
	protoUnknown traceProtocolState = iota
	// protoV1 means the Agent has advertised /v1.0/traces and nothing has
	// contradicted that since.
	protoV1
	// protoV04 is conclusive evidence v1 is unavailable: an /info poll
	// without /v1.0/traces, a 404 on /info, or a live v1 send rejected
	// outright. Terminal: advanceTraceProtocolState never moves off it.
	protoV04
)

// advanceTraceProtocolState raises c's trace-protocol state to at least s and
// reports whether it moved. A lower or equal s is a no-op — see
// traceProtocolState for why that's what makes concurrent evidence from the
// /info poller and the writer's send path order-independent.
func (c *config) advanceTraceProtocolState(s traceProtocolState) bool {
	for {
		cur := traceProtocolState(c.protocolState.Load())
		if cur >= s {
			return false
		}
		if c.protocolState.CompareAndSwap(int32(cur), int32(s)) {
			return true
		}
	}
}
