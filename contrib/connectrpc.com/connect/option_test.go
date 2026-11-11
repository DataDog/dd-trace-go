// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sync/atomic"
	"testing"

	connectrpc "connectrpc.com/connect"

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

func TestOptions(t *testing.T) {
	for _, test := range []struct {
		name  string
		opts  []Option
		check func(*testing.T, *config)
	}{
		{
			name: "defaults",
			check: func(t *testing.T, cfg *config) {
				assert.Nil(t, cfg.service.Load())
				assert.Equal(t, map[connectrpc.Code]bool{connectrpc.CodeCanceled: true}, cfg.nonErrorCodes)
				assert.Nil(t, cfg.errCheck)
				assert.True(t, cfg.traceStreamCalls)
				assert.True(t, cfg.traceStreamMessages)
				assert.False(t, cfg.noDebugStack)
				assert.Empty(t, cfg.untracedMethods)
				assert.False(t, cfg.withMetadataTags)
				assert.False(t, cfg.withRequestTags)
				assert.False(t, cfg.withErrorDetailTags)
				assert.True(t, math.IsNaN(cfg.analyticsRate))
				assert.Empty(t, cfg.spanOpts)
				assert.Nil(t, cfg.customTags)
				for _, key := range []string{"authorization", "cookie", "traceparent", "tracestate", "baggage", "b3", "x-datadog-trace-id", "x-api-key"} {
					assert.Contains(t, cfg.ignoredMetadata, key)
				}
			},
		},
		{
			name: "WithService", opts: []Option{WithService("svc")},
			check: func(t *testing.T, cfg *config) {
				require.NotNil(t, cfg.service.Load())
				assert.Equal(t, "svc", cfg.service.Load().name)
			},
		},
		{
			name: "WithStreamCalls and WithStreamMessages", opts: []Option{WithStreamCalls(false), WithStreamMessages(false)},
			check: func(t *testing.T, cfg *config) {
				assert.False(t, cfg.traceStreamCalls)
				assert.False(t, cfg.traceStreamMessages)
			},
		},
		{name: "NoDebugStack", opts: []Option{NoDebugStack()}, check: func(t *testing.T, cfg *config) { assert.True(t, cfg.noDebugStack) }},
		{
			name: "NonErrorCodes replaces the defaults", opts: []Option{NonErrorCodes(connectrpc.CodeNotFound, connectrpc.CodeAborted)},
			check: func(t *testing.T, cfg *config) {
				assert.Equal(t, map[connectrpc.Code]bool{connectrpc.CodeNotFound: true, connectrpc.CodeAborted: true}, cfg.nonErrorCodes)
			},
		},
		{name: "NonErrorCodes without codes", opts: []Option{NonErrorCodes()}, check: func(t *testing.T, cfg *config) { assert.Empty(t, cfg.nonErrorCodes) }},
		{
			name: "WithErrorCheck", opts: []Option{WithErrorCheck(func(string, error) bool { return false })},
			check: func(t *testing.T, cfg *config) { assert.NotNil(t, cfg.errCheck) },
		},
		{
			name: "WithUntracedMethods", opts: []Option{WithUntracedMethods(unaryProcedure, bidiProcedure)},
			check: func(t *testing.T, cfg *config) {
				assert.Equal(t, map[string]struct{}{unaryProcedure: {}, bidiProcedure: {}}, cfg.untracedMethods)
			},
		},
		{
			name: "WithIgnoredMetadata lowercases keys", opts: []Option{WithMetadataTags(), WithIgnoredMetadata("X-Secret")},
			check: func(t *testing.T, cfg *config) {
				assert.True(t, cfg.withMetadataTags)
				assert.Contains(t, cfg.ignoredMetadata, "x-secret")
				assert.Contains(t, cfg.ignoredMetadata, "authorization", "the defaults are kept")
			},
		},
		{
			name: "WithRequestTags and WithErrorDetailTags", opts: []Option{WithRequestTags(), WithErrorDetailTags()},
			check: func(t *testing.T, cfg *config) {
				assert.True(t, cfg.withRequestTags)
				assert.True(t, cfg.withErrorDetailTags)
			},
		},
		{
			name: "WithCustomTag", opts: []Option{WithCustomTag("a", 1), WithCustomTag("b", "two"), WithCustomTag("a", "one")},
			check: func(t *testing.T, cfg *config) {
				assert.Equal(t, map[string]any{"a": "one", "b": "two"}, cfg.tags)
				assert.NotNil(t, cfg.customTags)
			},
		},
		{
			name: "WithSpanOptions appends", opts: []Option{WithSpanOptions(tracer.Tag("a", 1)), WithSpanOptions(tracer.Tag("b", 2), tracer.Tag("c", 3))},
			check: func(t *testing.T, cfg *config) { assert.Len(t, cfg.spanOpts, 3) },
		},
		{
			name: "OptionFn", opts: []Option{OptionFn(func(cfg *config) { cfg.noDebugStack = true })},
			check: func(t *testing.T, cfg *config) { assert.True(t, cfg.noDebugStack) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.check(t, newConfig(test.opts...))
		})
	}
}

func TestService(t *testing.T) {
	t.Run("WithService", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		startAllSpanKinds(context.Background(), newConfig(WithService("svc")))
		spans := mt.FinishedSpans()
		require.Len(t, spans, 4)
		for _, span := range spans {
			assert.Equal(t, "svc", span.Tag(ext.ServiceName), span.OperationName())
			assert.Equal(t, "opt.with_service", span.Tag(ext.KeyServiceSource), span.OperationName())
		}
	})

	// With the mock tracer, which does not mark the tracer as started, the default is resolved for
	// every span.
	for _, test := range []struct {
		name        string
		configFirst bool
	}{
		{name: "defaults to the global service"},
		{name: "default is resolved when spans start", configFirst: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var cfg *config
			if test.configFirst {
				cfg = newConfig()
			}
			testutils.SetGlobalServiceName(t, "global-svc")
			if !test.configFirst {
				cfg = newConfig()
			}
			startAllSpanKinds(context.Background(), cfg)
			spans := mt.FinishedSpans()
			require.Len(t, spans, 4)
			for _, span := range spans {
				assert.Equal(t, "global-svc", span.Tag(ext.ServiceName), span.OperationName())
				// No service source is reported for the global service.
				assert.Nil(t, span.Tag(ext.KeyServiceSource), span.OperationName())
			}
		})
	}

	// An empty service on a message span would make the tracer mark it top-level, as the tracer
	// only then fills in its default service. Call spans, and server message spans without a call
	// span, get the empty service regardless, so that they don't inherit their parent's.
	for _, test := range []struct {
		name string
		opts []Option
		// wantReset is the set of spans that don't inherit the parent's service.
		wantReset map[string]bool
	}{
		{
			name:      "an empty default only applies to call spans",
			wantReset: map[string]bool{"client call": true, "server call": true},
		},
		{
			name: "an empty default applies to server message spans without a call span", opts: []Option{WithStreamCalls(false)},
			wantReset: map[string]bool{"client call": true, "server call": true, "server message": true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testutils.SetGlobalServiceName(t, "")
			mt := mocktracer.Start()
			defer mt.Stop()
			cfg := newConfig(test.opts...)
			parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent", tracer.ServiceName("parent-svc"))
			names := make([]string, 0, 4)
			for side, component := range map[string]instrumentation.Component{"client": instrumentation.ComponentClient, "server": instrumentation.ComponentServer} {
				call, _ := cfg.startCallSpan(ctx, component, bidiProcedure, nil, map[string]any{})
				call.Finish()
				cfg.startMessageSpan(ctx, messageTags(bidiSpec, &connectProtocol), component).Finish()
				names = append(names, side+" call", side+" message")
			}
			parent.Finish()
			spans := mt.FinishedSpans()
			require.Len(t, spans, len(names)+1)
			for i, name := range names {
				span := spans[i]
				if test.wantReset[name] {
					assert.NotEqual(t, "parent-svc", span.Tag(ext.ServiceName), name)
					assert.EqualValues(t, 1, span.Tag("_dd.top_level"), name)
					// The tracer's own default service is not an override.
					assert.Nil(t, span.Tag(ext.KeyServiceSource), name)
					continue
				}
				assert.Equal(t, "parent-svc", span.Tag(ext.ServiceName), name)
				assert.Nil(t, span.Tag("_dd.top_level"), name)
				if name == "server message" {
					assert.EqualValues(t, 1, span.Tag("_dd.measured"), name)
				}
			}
		})
	}

	// With the real tracer and no DD_SERVICE, the spans have the tracer's default service and no
	// service source.
	t.Run("empty default reports no service source", func(t *testing.T) {
		testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
		tr, agent, err := tracertest.Bootstrap(t, tracer.WithLogger(testutils.DiscardLogger()))
		require.NoError(t, err)
		require.Empty(t, newConfig().resolveService().name)
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

	// Orchestrion starts the tracer after package-level clients and handlers are built. This needs
	// the real tracer: the mock tracer never marks the tracer as started.
	t.Run("default is cached once the tracer has started", func(t *testing.T) {
		testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
		rig := newTestRig(t, []Option{WithCustomTag("side", "server")}, nil)
		cfg := newConfig()
		assert.Empty(t, cfg.resolveService().name)
		assert.Nil(t, cfg.service.Load(), "cached before the tracer started")

		tr, agent, err := tracertest.Bootstrap(t, tracer.WithService("started-svc"), tracer.WithLogger(testutils.DiscardLogger()))
		require.NoError(t, err)
		assert.Equal(t, "started-svc", cfg.resolveService().name)
		require.NotNil(t, cfg.service.Load(), "not cached after the tracer started")

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
	t.Run("precedence", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		cfg := newConfig(
			WithSpanOptions(
				tracer.Tag(ext.RPCMethod, "from-span-option"),
				tracer.Tag("both", "from-span-option"),
				tracer.SpanType("from-span-option"),
			),
			WithCustomTag(ext.ResourceName, "from-custom-tag"),
			WithCustomTag("both", "from-custom-tag"),
		)
		startAllSpanKinds(context.Background(), cfg)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 4)
		for _, span := range spans {
			// Custom tags beat the defaults (resource) and span options (both); span options beat
			// the per-call defaults (rpc.method) and the cached ones (span.type).
			assert.Equal(t, "from-custom-tag", span.Tag(ext.ResourceName), span.OperationName())
			assert.Equal(t, "from-custom-tag", span.Tag("both"), span.OperationName())
			assert.Equal(t, "from-span-option", span.Tag(ext.RPCMethod), span.OperationName())
			assert.Equal(t, "from-span-option", span.Tag(ext.SpanType), span.OperationName())
		}
	})

	// The tracer applies start tags in random map order, so a lost race shows over many spans.
	for _, test := range []struct {
		name string
		opts []Option
	}{
		{name: "WithSpanOptions", opts: []Option{WithSpanOptions(tracer.ServiceName("user-svc"))}},
		{name: "WithSpanOptions and WithService", opts: []Option{WithService("opt-svc"), WithSpanOptions(tracer.ServiceName("user-svc"))}},
		{name: "WithCustomTag and WithService", opts: []Option{WithService("opt-svc"), WithCustomTag(ext.ServiceName, "user-svc")}},
	} {
		t.Run("user service beats the interceptor's/"+test.name, func(t *testing.T) {
			testutils.SetGlobalServiceName(t, "global-svc")
			mt := mocktracer.Start()
			defer mt.Stop()
			cfg := newConfig(test.opts...)
			for range 50 {
				startAllSpanKinds(context.Background(), cfg)
			}
			spans := mt.FinishedSpans()
			require.Len(t, spans, 200)
			for _, span := range spans {
				require.Equal(t, "user-svc", span.Tag(ext.ServiceName), span.OperationName())
			}
		})
	}

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

func TestMetadataTags(t *testing.T) {
	header := http.Header{
		"X-Test":              {"a", "b"},
		"X-Secret":            {"hidden"},
		"X-Other":             {"other"},
		"Authorization":       {"Bearer secret"},
		"Cookie":              {"session=secret"},
		"Traceparent":         {"00-1-2-01"},
		"X-Datadog-Trace-Id":  {"1"},
		"B3":                  {"trace-span-1"},
		"Payload-Bin":         {"binary"},
		"Ot-Baggage-Secret":   {"secret"},
		"Proxy-Authorization": {"secret"},
	}
	for _, test := range []struct {
		name string
		opts []Option
		want map[string]any
	}{
		{name: "disabled by default", want: map[string]any{}},
		{
			name: "enabled", opts: []Option{WithMetadataTags()},
			want: map[string]any{
				"rpc.request.metadata.x-test.0":   "a",
				"rpc.request.metadata.x-test.1":   "b",
				"rpc.request.metadata.x-secret.0": "hidden",
				"rpc.request.metadata.x-other.0":  "other",
			},
		},
		{
			name: "ignored keys are case-insensitive", opts: []Option{WithMetadataTags(), WithIgnoredMetadata("X-SECRET", "x-other")},
			want: map[string]any{"rpc.request.metadata.x-test.0": "a", "rpc.request.metadata.x-test.1": "b"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			span := tracer.StartSpan("metadata")
			setMetadataTags(newConfig(test.opts...), header, span)
			span.Finish()
			assert.Equal(t, test.want, tagsWithPrefix(mt.FinishedSpans()[0], "rpc.request.metadata."))
		})
	}

	t.Run("ignored keys are per configuration", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		_ = newConfig(WithIgnoredMetadata("x-test"))
		span := tracer.StartSpan("metadata")
		setMetadataTags(newConfig(WithMetadataTags()), header, span)
		span.Finish()
		assert.Equal(t, "a", metadataTag(mt.FinishedSpans()[0], "x-test"))
	})

	t.Run("nil span", func(t *testing.T) {
		assert.NotPanics(t, func() { setMetadataTags(newConfig(WithMetadataTags()), header, nil) })
	})
}

// TestErrorCheckArguments checks what WithErrorCheck is called with: the procedure and the error,
// but only for errors that the NonErrorCodes rules did not already suppress.
func TestErrorCheckArguments(t *testing.T) {
	type call struct {
		procedure string
		err       error
	}
	boom := connectrpc.NewError(connectrpc.CodeInternal, errors.New("boom"))
	for _, test := range []struct {
		name string
		err  error
		want []call
	}{
		{name: "error", err: boom, want: []call{{procedure: unaryProcedure, err: boom}}},
		{name: "suppressed by code", err: connectrpc.NewError(connectrpc.CodeCanceled, errors.New("canceled"))},
		{name: "uncoded cancellation", err: context.Canceled},
		{name: "nil", err: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var calls []call
			cfg := newConfig(WithErrorCheck(func(procedure string, err error) bool {
				calls = append(calls, call{procedure: procedure, err: err})
				return true
			}))
			finishSpan(tracer.StartSpan("check"), test.err, unaryProcedure, &connectProtocol, finishMode{}, cfg)
			assert.Equal(t, test.want, calls)
		})
	}
}
