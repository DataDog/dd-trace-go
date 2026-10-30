// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/contrib/connectrpc.com/connectshared"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// TestOptions checks that each option configures the setting it documents; connectshared tests the
// settings themselves.
func TestOptions(t *testing.T) {
	for _, test := range []struct {
		name  string
		opts  []Option
		check func(*testing.T, *config)
	}{
		{
			name: "defaults",
			check: func(t *testing.T, cfg *config) {
				assert.True(t, cfg.core.StreamCalls())
				assert.True(t, cfg.core.StreamMessages())
				assert.True(t, traces(cfg, unaryProcedure))
				assert.False(t, cfg.core.MetadataTags())
				assert.False(t, cfg.core.RequestTags())
				// CodeCanceled is the only non-error code, no error check suppresses errors, and recorded
				// errors have a stack trace but no detail tags.
				assert.False(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeCanceled, errors.New("canceled"))))
				failed := finishedWith(t, cfg, errorWithDetails(connectrpc.CodeNotFound))
				assert.NotNil(t, failed.Tag(ext.ErrorMsg))
				assert.NotNil(t, failed.Tag(ext.ErrorHandlingStack))
				assert.Nil(t, failed.Tag("connect.status_details._0"))
				for _, span := range startedSpans(t, cfg) {
					assert.Equal(t, "connectrpc.com/connect", span.Tag(ext.Component), span.OperationName())
				}
			},
		},
		{
			name: "WithService", opts: []Option{WithService("svc")},
			check: func(t *testing.T, cfg *config) {
				for _, span := range startedSpans(t, cfg) {
					assert.Equal(t, "svc", span.Tag(ext.ServiceName), span.OperationName())
					assert.Equal(t, "opt.with_service", span.Tag(ext.KeyServiceSource), span.OperationName())
				}
			},
		},
		{
			name: "WithStreamCalls and WithStreamMessages", opts: []Option{WithStreamCalls(false), WithStreamMessages(false)},
			check: func(t *testing.T, cfg *config) {
				assert.False(t, cfg.core.StreamCalls())
				assert.False(t, cfg.core.StreamMessages())
			},
		},
		{name: "NoDebugStack", opts: []Option{NoDebugStack()}, check: assertNoDebugStack},
		{
			name: "NonErrorCodes replaces the defaults", opts: []Option{NonErrorCodes(connectrpc.CodeNotFound, connectrpc.CodeAborted)},
			check: func(t *testing.T, cfg *config) {
				assert.False(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeNotFound, errors.New("nope"))))
				assert.False(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeAborted, errors.New("aborted"))))
				assert.True(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeCanceled, errors.New("canceled"))))
			},
		},
		{
			name: "NonErrorCodes without codes", opts: []Option{NonErrorCodes()},
			check: func(t *testing.T, cfg *config) {
				assert.True(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeCanceled, errors.New("canceled"))))
			},
		},
		{
			name: "WithErrorCheck", opts: []Option{WithErrorCheck(func(string, error) bool { return false })},
			check: func(t *testing.T, cfg *config) {
				assert.False(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeInternal, errors.New("boom"))))
			},
		},
		{
			name: "WithUntracedMethods", opts: []Option{WithUntracedMethods(unaryProcedure, bidiProcedure)},
			check: func(t *testing.T, cfg *config) {
				assert.False(t, traces(cfg, unaryProcedure))
				assert.False(t, traces(cfg, bidiProcedure))
				assert.True(t, traces(cfg, clientStreamProcedure))
			},
		},
		{
			name: "WithIgnoredMetadata lowercases keys", opts: []Option{WithMetadataTags(), WithIgnoredMetadata("X-Secret")},
			check: func(t *testing.T, cfg *config) {
				assert.True(t, cfg.core.MetadataTags())
				mt := mocktracer.Start()
				t.Cleanup(mt.Stop)
				span := tracer.StartSpan("metadata")
				cfg.core.SetMetadataTags(span, maps.All(http.Header{"X-Test": {"a"}, "X-Secret": {"hidden"}, "Authorization": {"Bearer secret"}}))
				span.Finish()
				// The default ignored keys are kept.
				assert.Equal(t, map[string]any{tagRequestMetadataPrefix + "x-test.0": "a"}, tagsWithPrefix(mt.FinishedSpans()[0], tagRequestMetadataPrefix))
			},
		},
		{
			name: "WithRequestTags and WithErrorDetailTags", opts: []Option{WithRequestTags(), WithErrorDetailTags()},
			check: func(t *testing.T, cfg *config) {
				assert.True(t, cfg.core.RequestTags())
				assert.Equal(t, "seconds:1", finishedWith(t, cfg, errorWithDetails(connectrpc.CodeInternal)).Tag("connect.status_details._0"))
			},
		},
		{
			name: "WithCustomTag", opts: []Option{WithCustomTag("a", 1), WithCustomTag("b", "two"), WithCustomTag("a", "one")},
			check: func(t *testing.T, cfg *config) {
				for _, span := range startedSpans(t, cfg) {
					assert.Equal(t, "one", span.Tag("a"), span.OperationName())
					assert.Equal(t, "two", span.Tag("b"), span.OperationName())
				}
			},
		},
		{
			name: "WithSpanOptions appends", opts: []Option{WithSpanOptions(tracer.Tag("a", 1)), WithSpanOptions(tracer.Tag("b", 2), tracer.Tag("c", 3))},
			check: func(t *testing.T, cfg *config) {
				// The tracer stores numeric tags as float64 metrics.
				for _, span := range startedSpans(t, cfg) {
					for key, value := range map[string]any{"a": 1.0, "b": 2.0, "c": 3.0} {
						assert.Equal(t, value, span.Tag(key), span.OperationName())
					}
				}
			},
		},
		{
			name: "OptionFn", opts: []Option{OptionFn(func(cfg *config) { cfg.core.SetNoDebugStack() })},
			check: assertNoDebugStack,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.check(t, newConfig(test.opts...))
		})
	}
}

// traces reports whether an interceptor configured with cfg traces a call to procedure.
func traces(cfg *config, procedure string) bool {
	_, traced := cfg.core.ClaimCall(context.Background(), instrumentation.ComponentServer, procedure, nil)
	return traced
}

// finishedWith returns the call span of a unary RPC over Connect that cfg finishes with err.
func finishedWith(t *testing.T, cfg *config, err error) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	cfg.core.FinishSpan(tracer.StartSpan("finish"), err, connectshared.SpanFinish{Procedure: unaryProcedure, Protocol: connectProtocol})
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}

// recordsError reports whether cfg records err on the span it finishes with it.
func recordsError(t *testing.T, cfg *config, err error) bool {
	t.Helper()
	return finishedWith(t, cfg, err).Tag(ext.ErrorMsg) != nil
}

// assertNoDebugStack checks that cfg records errors without a stack trace.
func assertNoDebugStack(t *testing.T, cfg *config) {
	failed := finishedWith(t, cfg, connectrpc.NewError(connectrpc.CodeInternal, errors.New("boom")))
	assert.NotNil(t, failed.Tag(ext.ErrorMsg))
	assert.Nil(t, failed.Tag(ext.ErrorHandlingStack))
}

// startedSpans returns the four spans that startAllSpanKinds starts with cfg.
func startedSpans(t *testing.T, cfg *config) []*mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	startAllSpanKinds(context.Background(), cfg)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 4)
	return spans
}

func TestService(t *testing.T) {
	// With the real tracer and no DD_SERVICE, the spans have the tracer's default service and no
	// service source.
	t.Run("empty default reports no service source", func(t *testing.T) {
		testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
		tr, agent, err := tracertest.Bootstrap(t, tracer.WithLogger(testutils.DiscardLogger()))
		require.NoError(t, err)
		require.Empty(t, instr.ServiceName(instrumentation.ComponentDefault, nil))
		rig := newTestRig(t, nil, nil)
		_, err = rig.client(unaryProcedure).CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
		require.NoError(t, err)
		runBidi(t, rig.client(bidiProcedure).CallBidiStream(context.Background()))
		tr.Flush()
		require.Equal(t, 10, agent.CountSpans())
		assert.Nil(t, agent.FindSpan(agenttest.With().Condition("has a service source", func(s *agenttest.Span) bool {
			_, ok := s.Meta[ext.KeyServiceSource]
			return ok
		})))
	})

	// With the real tracer and no DD_SERVICE, message spans have the tracer's default service, not
	// that of a local parent such as an HTTP server span. Under a call span they are not top-level,
	// and the server's are measured; without one, they are the entry points of their service.
	for _, test := range []struct {
		name  string
		calls bool
		spans int
	}{{name: "with stream calls", calls: true, spans: 9}, {name: "without stream calls", spans: 7}} {
		t.Run("message spans of the default service/"+test.name, func(t *testing.T) {
			testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
			tr, agent, err := tracertest.Bootstrap(t, tracer.WithLogger(testutils.DiscardLogger()))
			require.NoError(t, err)
			rig := newTestRig(t, []Option{WithStreamCalls(test.calls), WithCustomTag("side", "server")}, []Option{WithStreamCalls(test.calls)})
			parent, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
				tracer.ServiceName("http.router"), tracer.SpanType(ext.SpanTypeWeb), tracer.Tag(ext.SpanKind, ext.SpanKindServer))
			runBidi(t, rig.client(bidiProcedure).CallBidiStream(ctx))
			parent.Finish()
			tr.Flush()
			require.Equal(t, test.spans, agent.CountSpans())
			messages := 0
			for _, span := range agent.(agenttest.SpanLister).Spans() {
				if span.Operation != operationMessage {
					continue
				}
				messages++
				side := cmp.Or(span.Meta["side"], "client")
				assert.Equal(t, filepath.Base(os.Args[0]), span.Service, side)
				_, topLevel := span.Metrics["_dd.top_level"]
				assert.Equal(t, !test.calls, topLevel, "%s: _dd.top_level", side)
				assert.Equal(t, test.calls && side == "server", span.Metrics["_dd.measured"] == 1, "%s: _dd.measured", side)
			}
			assert.Equal(t, 6, messages)
		})
	}

	// Orchestrion starts the tracer after package-level clients and handlers are built. This needs
	// the real tracer: the mock tracer never marks the tracer as started.
	t.Run("default is cached once the tracer has started", func(t *testing.T) {
		testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
		rig := newTestRig(t, []Option{WithCustomTag("side", "server")}, nil)
		tr, agent, err := tracertest.Bootstrap(t, tracer.WithService("started-svc"), tracer.WithLogger(testutils.DiscardLogger()))
		require.NoError(t, err)
		runBidi(t, rig.client(bidiProcedure).CallBidiStream(context.Background()))
		tr.Flush()
		require.Equal(t, 8, agent.CountSpans())
		assert.Nil(t, agent.FindSpan(agenttest.With().Condition("service is not started-svc", func(s *agenttest.Span) bool {
			return s.Service != "started-svc"
		})))
		assert.Nil(t, agent.FindSpan(agenttest.With().Operation(operationMessage).Condition("top-level", func(s *agenttest.Span) bool {
			_, ok := s.Metrics["_dd.top_level"]
			return ok
		})))
		agent.RequireSpan(t, agenttest.With().Operation(operationMessage).Tag("side", "server"))
		assert.Nil(t, agent.FindSpan(agenttest.With().Operation(operationMessage).Tag("side", "server").Condition("not measured", func(s *agenttest.Span) bool {
			return s.Metrics["_dd.measured"] != 1
		})))
	})
}

func TestAnalytics(t *testing.T) {
	for _, test := range []struct {
		name string
		env  string
		want any
	}{
		{name: "disabled by default"},
		{name: "enabled by the environment", env: "true", want: 1.0},
		{name: "disabled by the environment", env: "false"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.env != "" {
				t.Setenv("DD_TRACE_CONNECT_ANALYTICS_ENABLED", test.env)
			}
			mt := mocktracer.Start()
			defer mt.Stop()
			rig := newTestRig(t, nil, nil)
			_, err := rig.client(unaryProcedure).CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
			require.NoError(t, err)
			requireCallPair(t, mt, methodKindUnary)
			startAllSpanKinds(context.Background(), newConfig())
			spans := mt.FinishedSpans()
			require.Len(t, spans, 6)
			for _, span := range spans {
				assert.Equal(t, test.want, span.Tag(ext.EventSampleRate), span.OperationName())
			}
		})
	}
}

func TestSpanOptions(t *testing.T) {
	t.Run("parent beats the propagated parent", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		propagated, user := tracer.StartSpan("propagated"), tracer.StartSpan("user")
		conn := newFakeHandlerConn()
		require.NoError(t, tracer.Inject(propagated.Context(), tracer.HTTPHeadersCarrier(conn.header)))
		require.NoError(t, traceFakeHandler(context.Background(), conn, sendMessage,
			WithSpanOptions(tracer.ChildOf(user.Context())), WithStreamMessages(false)))
		calls := spansNamed(mt.FinishedSpans(), operationServer)
		require.Len(t, calls, 1)
		assert.Equal(t, user.Context().SpanID(), calls[0].ParentID())
	})

	// Span options may be stateful, so they must not run to build the configuration or to check
	// whether a span is a local root.
	t.Run("run once per span", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		var calls atomic.Int32
		counting := WithSpanOptions(func(*tracer.StartSpanConfig) { calls.Add(1) })
		cfg := newConfig(counting)
		require.Zero(t, calls.Load(), "newConfig ran a span option")
		for range 5 {
			startAllSpanKinds(context.Background(), cfg)
		}
		assert.EqualValues(t, 20, calls.Load())

		// Without call spans, every message span is a local root.
		calls.Store(0)
		mt.Reset()
		opts := []Option{counting, WithStreamCalls(false)}
		rig := newTestRig(t, opts, opts)
		runBidi(t, rig.client(bidiProcedure).CallBidiStream(context.Background()))
		assert.Len(t, mt.FinishedSpans(), 6)
		assert.EqualValues(t, 6, calls.Load())
	})
}
