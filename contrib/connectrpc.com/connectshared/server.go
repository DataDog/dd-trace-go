// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// StartServerCall starts the call span of an RPC of spec that a handler serves over protocol, as a
// child of the span context propagated in carrier, and marks the returned context as handling the
// call that id identifies (see ClaimCall).
func (c *Config) StartServerCall(ctx context.Context, spec Spec, protocol *Protocol, id any, carrier tracer.TextMapReader) (*tracer.Span, context.Context) {
	parentOpts, _ := propagationOptions(carrier)
	return c.StartCallSpan(ctx, instrumentation.ComponentServer, spec, protocol, id, parentOpts...)
}

var messageFinishMode = FinishMode{AllowEOF: true}

// ServerMessages starts the message spans of a streaming handler. The handler's stream wrapper
// holds it in a field, and calls Init before the handler runs.
type ServerMessages struct {
	spans  messageSpans
	finish SpanFinish
	// snapshot is a copy of the request headers when there is no call span, taken before the
	// handler runs: message spans extract their parent from it, and the first one is tagged with
	// its metadata. The headers themselves are not read again, as the handler may modify them
	// concurrently.
	snapshot       tracer.TextMapReader
	metadataTagged atomic.Bool
	// unpropagated is set once the tracer has failed to extract a trace context from the snapshot,
	// which then can't change.
	unpropagated atomic.Bool
}

// Init prepares m for the messages of an RPC of spec over protocol whose handler runs with ctx.
// snapshot is only read when streaming RPCs have no call span: it must then be a copy of the
// request headers.
func (m *ServerMessages) Init(ctx context.Context, cfg *Config, spec Spec, protocol *Protocol, snapshot tracer.TextMapReader) {
	m.spans.init(ctx, cfg, instrumentation.ComponentServer, spec, protocol)
	m.finish = SpanFinish{Procedure: spec.Procedure, Protocol: protocol, Mode: messageFinishMode}
	m.snapshot = snapshot
}

// Start starts a message span. Without a call span, it extracts the span's parent afresh from the
// snapshot, so that an explicit span in the handler's context still wins but an Orchestrion GLS
// fallback does not. tagMetadata is set for the stream's first message span without a call span:
// the caller then tags it with the snapshot's metadata through SetMetadataTags. Ranging here over
// an iterator that the compiler doesn't know would allocate.
func (m *ServerMessages) Start() (span *tracer.Span, tagMetadata bool) {
	cfg := m.spans.cfg
	var parentOpts []tracer.StartSpanOption
	if !cfg.traceStreamCalls && !m.unpropagated.Load() {
		var err error
		if parentOpts, err = propagationOptions(m.snapshot); err != nil {
			m.unpropagated.Store(true)
		}
	}
	span = m.spans.start(PeerTags{}, parentOpts...)
	return span, cfg.withMetadataTags && !cfg.traceStreamCalls && m.metadataTagged.CompareAndSwap(false, true)
}

// SpanFinish returns how the message spans are classified when they finish, for FinishOnReturn.
func (m *ServerMessages) SpanFinish() *SpanFinish {
	return &m.finish
}

// TagRequest tags span with request, a message that the handler received, if EnableRequestTags
// was called.
func (m *ServerMessages) TagRequest(span *tracer.Span, request any) {
	m.spans.cfg.SetRequestTags(span, request, m.finish.Protocol)
}
