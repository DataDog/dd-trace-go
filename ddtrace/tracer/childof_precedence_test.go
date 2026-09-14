// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartSpanFromContextParentPrecedence pins how StartSpanFromContext
// resolves a conflict between the parent a caller passes as ChildOf and the
// active span it finds in the context.
//
// The two are not equivalent, which is the distinction this test exists to hold:
//
//   - ContextWithSpan snapshots the span's SpanContext under
//     activeSpanContextKey. A span reachable that way was deliberately placed in
//     the context, so it outranks ChildOf. TestStartSpanFromContext has asserted
//     this since 2018 and it is unchanged.
//   - A span reachable only under internal.ActiveSpanKey was not put there by
//     ContextWithSpan. Under Orchestrion that is the goroutine-local storage
//     fallback: ctx.Value consults the GLS stack, so an active span resolves
//     even from context.Background(). That is an inference about which scope we
//     are in, and it must yield to a parent the caller named.
//
// The second case is why ten integrations were losing wire-extracted parents.
// They extract a producer or upstream span from the message headers and pass it
// as ChildOf, then call StartSpanFromContext with whatever ambient context they
// hold, for example:
//
//	contrib/confluentinc/confluent-kafka-go/kafkatrace/consumer.go:83-90
//	contrib/twmb/franz-go/kgo.go:150-156
//	contrib/segmentio/kafka-go/internal/tracing/tracing.go:51-53
//	contrib/google.golang.org/grpc/grpc.go:77-79
//
// With the GLS supplying an ambient span on nearly every call, the wire parent
// was discarded and the message attached to unrelated work instead of to its
// producer.
//
// The ChildOf(nil) case is not hypothetical: tracer.Extract returns (nil, nil)
// when tracing is disabled and under
// DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT=ignore, and the segmentio and grpc call
// sites above pass the result to ChildOf without a nil check. ChildOf cannot
// express "start a root span", so a nil parent has to keep counting as unset or
// those spans would lose their ambient parent entirely.
func TestStartSpanFromContextParentPrecedence(t *testing.T) {
	// The parent extracted from the wire: a producer span, or an upstream RPC
	// caller. This is the one the new trace should attach to.
	explicit := &Span{context: &SpanContext{
		spanID:  1111,
		traceID: traceIDFrom64Bits(9999),
	}}
	// An unrelated span that happens to be active in this scope.
	ambient := &Span{context: &SpanContext{
		spanID:  2222,
		traceID: traceIDFrom64Bits(8888),
	}}

	// glsShape reproduces what Orchestrion's GLS fallback yields: ctx.Value
	// resolves an active span, but nothing snapshotted it under
	// activeSpanContextKey because ContextWithSpan was never called.
	glsShape := func(s *Span) context.Context {
		return context.WithValue(context.Background(), internal.ActiveSpanKey, s)
	}

	for _, tt := range []struct {
		name         string
		ctx          context.Context
		opts         []StartSpanOption
		wantParentID uint64
		wantTraceID  uint64
	}{
		{
			name:         "inferred parent yields to explicit ChildOf",
			ctx:          glsShape(ambient),
			opts:         []StartSpanOption{ChildOf(explicit.Context())},
			wantParentID: 1111,
			wantTraceID:  9999,
		},
		{
			name:         "inferred parent survives a nil ChildOf",
			ctx:          glsShape(ambient),
			opts:         []StartSpanOption{ChildOf(nil)},
			wantParentID: 2222,
			wantTraceID:  8888,
		},
		{
			name:         "inferred parent applies when no ChildOf is passed",
			ctx:          glsShape(ambient),
			wantParentID: 2222,
			wantTraceID:  8888,
		},
		{
			name:         "snapshotted parent overrides explicit ChildOf",
			ctx:          ContextWithSpan(context.Background(), ambient),
			opts:         []StartSpanOption{ChildOf(explicit.Context())},
			wantParentID: 2222,
			wantTraceID:  8888,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, stop, err := startTestTracer(t)
			require.NoError(t, err)
			defer stop()

			got, _ := StartSpanFromContext(tt.ctx, "kafka.consume", tt.opts...)
			assert.Equal(t, tt.wantParentID, got.parentID)
			assert.Equal(t, tt.wantTraceID, got.traceID)
		})
	}
}
