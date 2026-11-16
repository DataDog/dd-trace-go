// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"
	"maps"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// propagatedHeader returns request headers that propagate a finished span, and that span.
func propagatedHeader(t *testing.T) (http.Header, *tracer.Span) {
	t.Helper()
	propagated := tracer.StartSpan("propagated")
	propagated.Finish()
	header := http.Header{"X-Meta": {"value"}}
	require.NoError(t, tracer.Inject(propagated.Context(), tracer.HTTPHeadersCarrier(header)))
	return header, propagated
}

func TestStartServerCall(t *testing.T) {
	t.Run("continues the propagated trace", func(t *testing.T) {
		mt := startMockTracer(t)
		header, propagated := propagatedHeader(t)
		cfg := newTestConfig()
		call := &testCall{1}
		span, ctx := cfg.StartServerCall(context.Background(), Spec{Procedure: unaryProcedure}, &grpcProtocol, call, tracer.HTTPHeadersCarrier(header))
		_, traced := cfg.ClaimCall(ctx, instrumentation.ComponentServer, unaryProcedure, call)
		assert.False(t, traced)
		span.Finish()
		servers := spansNamed(mt.FinishedSpans(), operationServer)
		require.Len(t, servers, 1)
		assert.Equal(t, propagated.Context().SpanID(), servers[0].ParentID())
		assert.Equal(t, ext.SpanKindServer, servers[0].Tag(ext.SpanKind))
		assert.Equal(t, "grpc", servers[0].Tag(ext.RPCSystem))
		assert.Equal(t, unaryProcedure, servers[0].Tag(ext.GRPCFullMethod))
		// The metadata is tagged separately.
		assert.Nil(t, servers[0].Tag(tagRequestMetadataPrefix+"x-meta.0"))
	})

	t.Run("without a propagated trace", func(t *testing.T) {
		mt := startMockTracer(t)
		span, _ := newTestConfig().StartServerCall(context.Background(), bidiSpec, &connectProtocol, nil, tracer.HTTPHeadersCarrier(http.Header{}))
		span.Finish()
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Zero(t, spans[0].ParentID())
	})
}

// countingCarrier counts the times that the tracer reads it.
type countingCarrier struct {
	tracer.TextMapReader
	reads atomic.Int32
}

func (c *countingCarrier) ForeachKey(handler func(key, value string) error) error {
	c.reads.Add(1)
	return c.TextMapReader.ForeachKey(handler)
}

// testServerStream is a stream wrapper of a handler, over Connect.
type testServerStream struct {
	cfg      *Config
	messages ServerMessages
	header   http.Header
}

func newTestServerStream(ctx context.Context, cfg *Config, header http.Header) *testServerStream {
	s := &testServerStream{cfg: cfg}
	if !cfg.StreamCalls() {
		s.header = header.Clone()
	}
	s.messages.Init(ctx, cfg, bidiSpec, &connectProtocol, tracer.HTTPHeadersCarrier(s.header))
	return s
}

func (s *testServerStream) startMessageSpan() *tracer.Span {
	span, tagMetadata := s.messages.Start()
	if tagMetadata {
		s.cfg.SetMetadataTags(span, maps.All(s.header))
	}
	return span
}

// startServerMessages starts and finishes the message spans of a handler that receives
// wrapperspb.String("hello") and sends a message, with ctx and header.
func startServerMessages(ctx context.Context, cfg *Config, header http.Header) {
	stream := newTestServerStream(ctx, cfg, header)
	received := stream.startMessageSpan()
	stream.messages.TagRequest(received, wrapperspb.String("hello"))
	received.Finish()
	stream.startMessageSpan().Finish()
}

func TestServerMessages(t *testing.T) {
	t.Run("with a call span", func(t *testing.T) {
		mt := startMockTracer(t)
		header, _ := propagatedHeader(t)
		cfg := newTestConfig((*Config).EnableMetadataTags, (*Config).EnableRequestTags)
		call, ctx := cfg.StartServerCall(context.Background(), bidiSpec, &connectProtocol, nil, tracer.HTTPHeadersCarrier(header))
		startServerMessages(ctx, cfg, header)
		call.Finish()
		messages := spansNamed(mt.FinishedSpans(), operationMessage)
		require.Len(t, messages, 2)
		for _, span := range messages {
			assert.Equal(t, call.Context().SpanID(), span.ParentID())
			assert.Equal(t, "connectrpc", span.Tag(ext.RPCSystem))
			assert.Equal(t, bidiProcedure, span.Tag(ext.ResourceName))
			// The call span carries the metadata.
			assert.Empty(t, tagsWithPrefix(span, tagRequestMetadataPrefix))
		}
		assert.Equal(t, `"hello"`, messages[0].Tag(tagConnectRequest))
		assert.Nil(t, messages[1].Tag(tagConnectRequest))
	})

	t.Run("without a call span", func(t *testing.T) {
		mt := startMockTracer(t)
		header, propagated := propagatedHeader(t)
		cfg := newTestConfig((*Config).EnableMetadataTags, func(c *Config) { c.SetStreamCalls(false) })
		startServerMessages(context.Background(), cfg, header)
		messages := spansNamed(mt.FinishedSpans(), operationMessage)
		require.Len(t, messages, 2)
		// Each message span extracts the propagated parent afresh.
		for _, span := range messages {
			assert.Equal(t, propagated.Context().SpanID(), span.ParentID())
		}
		// Only the first message span carries the metadata.
		assert.Equal(t, map[string]any{tagRequestMetadataPrefix + "x-meta.0": "value"}, tagsWithPrefix(messages[0], tagRequestMetadataPrefix))
		assert.Empty(t, tagsWithPrefix(messages[1], tagRequestMetadataPrefix))
		// Request tags are disabled.
		assert.Nil(t, messages[0].Tag(tagConnectRequest))
	})

	t.Run("without a call span or metadata tags", func(t *testing.T) {
		mt := startMockTracer(t)
		header, _ := propagatedHeader(t)
		startServerMessages(context.Background(), newTestConfig(func(c *Config) { c.SetStreamCalls(false) }), header)
		for _, span := range spansNamed(mt.FinishedSpans(), operationMessage) {
			assert.Empty(t, tagsWithPrefix(span, tagRequestMetadataPrefix))
		}
	})

	// A span in the handler's context is explicit, unlike the trace that Orchestrion finds through
	// goroutine-local storage.
	t.Run("a span in the handler's context beats the propagated parent", func(t *testing.T) {
		mt := startMockTracer(t)
		header, _ := propagatedHeader(t)
		parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
		startServerMessages(ctx, newTestConfig(func(c *Config) { c.SetStreamCalls(false) }), header)
		parent.Finish()
		for _, span := range spansNamed(mt.FinishedSpans(), operationMessage) {
			assert.Equal(t, parent.Context().SpanID(), span.ParentID())
		}
	})

	// The snapshot is never modified, so once the tracer has failed to extract a trace context from
	// it, later message spans don't extract again.
	t.Run("extract their parent until the snapshot has none", func(t *testing.T) {
		const messages = 3
		for _, test := range []struct {
			name        string
			propagated  bool
			streamCalls bool
			// extractions is the number of times that the message spans extract their parent.
			extractions int32
		}{
			{name: "afresh for every message", propagated: true, extractions: messages},
			{name: "once without a propagated trace", extractions: 1},
			{name: "never with a call span", propagated: true, streamCalls: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				mt := startMockTracer(t)
				header := http.Header{}
				var propagated *tracer.Span
				if test.propagated {
					header, propagated = propagatedHeader(t)
				}
				extraction := &countingCarrier{TextMapReader: tracer.HTTPHeadersCarrier(header)}
				_, _ = tracer.Extract(extraction)
				require.NotZero(t, extraction.reads.Load())
				carrier := &countingCarrier{TextMapReader: tracer.HTTPHeadersCarrier(header)}
				var stream ServerMessages
				stream.Init(context.Background(), newTestConfig(func(c *Config) { c.SetStreamCalls(test.streamCalls) }), bidiSpec, &connectProtocol, carrier)
				for range messages {
					span, _ := stream.Start()
					span.Finish()
				}
				assert.Equal(t, test.extractions*extraction.reads.Load(), carrier.reads.Load())
				spans := spansNamed(mt.FinishedSpans(), operationMessage)
				require.Len(t, spans, messages)
				for _, span := range spans {
					if test.extractions > 0 && test.propagated {
						assert.Equal(t, propagated.Context().SpanID(), span.ParentID())
					} else {
						assert.Zero(t, span.ParentID())
					}
				}
			})
		}
	})

	// While tracing is disabled, the tracer extracts nothing without failing.
	t.Run("extract their parent once tracing starts", func(t *testing.T) {
		mt := mocktracer.Start()
		header, propagated := propagatedHeader(t)
		mt.Stop()
		var stream ServerMessages
		stream.Init(context.Background(), newTestConfig(func(c *Config) { c.SetStreamCalls(false) }), bidiSpec, &connectProtocol, tracer.HTTPHeadersCarrier(header))
		span, _ := stream.Start()
		require.Nil(t, span)
		mt = startMockTracer(t)
		span, _ = stream.Start()
		span.Finish()
		spans := spansNamed(mt.FinishedSpans(), operationMessage)
		require.Len(t, spans, 1)
		assert.Equal(t, propagated.Context().SpanID(), spans[0].ParentID())
	})

	t.Run("message spans allow the end of the stream", func(t *testing.T) {
		var messages ServerMessages
		messages.Init(context.Background(), newTestConfig(), bidiSpec, &grpcProtocol, nil)
		assert.Equal(t, SpanFinish{Procedure: bidiProcedure, Protocol: &grpcProtocol, Mode: FinishMode{AllowEOF: true}}, *messages.SpanFinish())
	})

	t.Run("concurrent messages tag the metadata once", func(t *testing.T) {
		mt := startMockTracer(t)
		cfg := newTestConfig((*Config).EnableMetadataTags, func(c *Config) { c.SetStreamCalls(false) })
		stream := newTestServerStream(context.Background(), cfg, http.Header{"X-Meta": {"value"}})
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() { stream.startMessageSpan().Finish() })
		}
		wg.Wait()
		var tagged int
		for _, span := range mt.FinishedSpans() {
			if len(tagsWithPrefix(span, tagRequestMetadataPrefix)) > 0 {
				tagged++
			}
		}
		assert.Equal(t, 1, tagged)
	})

	t.Run("only the first message span without a call span is tagged with the metadata", func(t *testing.T) {
		startMockTracer(t)
		for _, test := range []struct {
			name    string
			setters []func(*Config)
			want    []bool
		}{
			{name: "with a call span", setters: []func(*Config){(*Config).EnableMetadataTags}, want: []bool{false, false}},
			{name: "without a call span", setters: []func(*Config){(*Config).EnableMetadataTags, func(c *Config) { c.SetStreamCalls(false) }}, want: []bool{true, false}},
			{name: "without metadata tags", setters: []func(*Config){func(c *Config) { c.SetStreamCalls(false) }}, want: []bool{false, false}},
		} {
			var messages ServerMessages
			messages.Init(context.Background(), newTestConfig(test.setters...), bidiSpec, &connectProtocol, tracer.HTTPHeadersCarrier(http.Header{}))
			var got []bool
			for range test.want {
				span, tagMetadata := messages.Start()
				span.Finish()
				got = append(got, tagMetadata)
			}
			assert.Equal(t, test.want, got, test.name)
		}
	})
}
