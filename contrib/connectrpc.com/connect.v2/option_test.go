// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"

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
)

// flushUntil flushes the tracer until the agent has received n spans: a server span may
// finish after the client's call has returned.
func flushUntil(t *testing.T, tr tracer.Tracer, agent agenttest.Agent, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		tr.Flush()
		return agent.CountSpans() >= n
	}, 5*time.Second, time.Millisecond)
	require.Equal(t, n, agent.CountSpans())
}

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
				assert.False(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeCanceled, "canceled")))
				failed := finishedWith(t, cfg, errorWithDetails(connectrpc.CodeNotFound))
				assert.NotNil(t, failed.Tag(ext.ErrorMsg))
				assert.NotNil(t, failed.Tag(ext.ErrorHandlingStack))
				assert.Nil(t, failed.Tag("connect.status_details._0"))
				for _, span := range startedSpans(t, cfg) {
					assert.Equal(t, "connectrpc.com/connect.v2", span.Tag(ext.Component), span.OperationName())
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
				assert.False(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeNotFound, "nope")))
				assert.False(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeAborted, "aborted")))
				assert.True(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeCanceled, "canceled")))
			},
		},
		{
			name: "NonErrorCodes without codes", opts: []Option{NonErrorCodes()},
			check: func(t *testing.T, cfg *config) {
				assert.True(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeCanceled, "canceled")))
			},
		},
		{
			name: "WithErrorCheck", opts: []Option{WithErrorCheck(func(string, error) bool { return false })},
			check: func(t *testing.T, cfg *config) {
				assert.False(t, recordsError(t, cfg, connectrpc.NewError(connectrpc.CodeInternal, "boom")))
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
			name: "WithUntracedMethods replaces earlier calls", opts: []Option{WithUntracedMethods(unaryProcedure), WithUntracedMethods(bidiProcedure)},
			check: func(t *testing.T, cfg *config) {
				assert.True(t, traces(cfg, unaryProcedure))
				assert.False(t, traces(cfg, bidiProcedure))
			},
		},
		{
			name: "WithIgnoredMetadata lowercases keys", opts: []Option{WithMetadataTags(), WithIgnoredMetadata("X-Secret")},
			check: func(t *testing.T, cfg *config) {
				assert.True(t, cfg.core.MetadataTags())
				header := new(connectrpc.Header)
				for key, value := range map[string]string{"X-Test": "a", "X-Secret": "hidden", "Authorization": "Bearer secret"} {
					header.Set(key, value)
				}
				mt := startMockTracer(t)
				span := tracer.StartSpan("metadata")
				cfg.core.SetMetadataTags(span, header.All())
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
	mt := startMockTracer(t)
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
	failed := finishedWith(t, cfg, connectrpc.NewError(connectrpc.CodeInternal, "boom"))
	assert.NotNil(t, failed.Tag(ext.ErrorMsg))
	assert.Nil(t, failed.Tag(ext.ErrorHandlingStack))
}

// startedSpans returns the four spans that startAllSpanKinds starts with cfg.
func startedSpans(t *testing.T, cfg *config) []*mocktracer.Span {
	t.Helper()
	mt := startMockTracer(t)
	startAllSpanKinds(context.Background(), cfg)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 4)
	return spans
}

func TestService(t *testing.T) {
	// The in-process server's context derives from the client's, and its spans must not inherit
	// the client's service.
	t.Run("WithService on each side", func(t *testing.T) {
		for _, tt := range testTransports {
			t.Run(tt.name, func(t *testing.T) {
				mt := startMockTracer(t)
				_, client := newTracedRig(t, tt, []Option{WithService("server-svc")}, []Option{WithService("client-svc")})
				_, err := callBidi(context.Background(), client, "hello")
				require.NoError(t, err)
				clientCall, serverCall := requireCallPair(t, mt, methodKindBidiStream)
				spans := mt.FinishedSpans()
				require.Len(t, spans, 8)
				for _, side := range []struct {
					call    *mocktracer.Span
					service string
				}{{clientCall, "client-svc"}, {serverCall, "server-svc"}} {
					sideSpans := append(spansNamed(childrenOf(spans, side.call), operationMessage), side.call)
					require.Len(t, sideSpans, 4, side.service)
					for _, span := range sideSpans {
						assert.Equal(t, side.service, span.Tag(ext.ServiceName), span.OperationName())
						assert.Equal(t, "opt.with_service", span.Tag(ext.KeyServiceSource), span.OperationName())
					}
				}
			})
		}
	})

	// With the real tracer and no DD_SERVICE, the spans have the tracer's default service and no
	// service source.
	t.Run("empty default reports no service source", func(t *testing.T) {
		for _, tt := range testTransports {
			t.Run(tt.name, func(t *testing.T) {
				testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
				tr, agent, err := tracertest.Bootstrap(t, tracer.WithLogger(testutils.DiscardLogger()))
				require.NoError(t, err)
				require.Empty(t, instr.ServiceName(instrumentation.ComponentDefault, nil))
				_, client := newTracedRig(t, tt, nil, nil)
				_, err = callUnary(context.Background(), client, "hello")
				require.NoError(t, err)
				_, err = callBidi(context.Background(), client, "hello")
				require.NoError(t, err)
				flushUntil(t, tr, agent, 10)
				assert.Nil(t, agent.FindSpan(agenttest.With().Condition("has a service source", func(s *agenttest.Span) bool {
					_, ok := s.Meta[ext.KeyServiceSource]
					return ok
				})))
			})
		}
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
			for _, tt := range testTransports {
				t.Run(tt.name, func(t *testing.T) {
					testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
					tr, agent, err := tracertest.Bootstrap(t, tracer.WithLogger(testutils.DiscardLogger()))
					require.NoError(t, err)
					_, client := newTracedRig(t, tt, []Option{WithStreamCalls(test.calls), WithCustomTag("side", "server")}, []Option{WithStreamCalls(test.calls)})
					parent, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
						tracer.ServiceName("http.router"), tracer.SpanType(ext.SpanTypeWeb), tracer.Tag(ext.SpanKind, ext.SpanKindServer))
					_, err = callBidi(ctx, client, "hello")
					require.NoError(t, err)
					parent.Finish()
					flushUntil(t, tr, agent, test.spans)
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
		})
	}

	// Orchestrion starts the tracer after package-level clients and servers are built. This needs
	// the real tracer: the mock tracer never marks the tracer as started.
	for _, source := range []struct {
		name string
		env  string
		opts []tracer.StartOption
	}{
		{name: "tracer.WithService", opts: []tracer.StartOption{tracer.WithService("started-svc")}},
		{name: "DD_SERVICE", env: "started-svc"},
	} {
		t.Run("default is cached once the tracer has started/"+source.name, func(t *testing.T) {
			for _, tt := range testTransports {
				t.Run(tt.name, func(t *testing.T) {
					testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
					if source.env != "" {
						t.Setenv("DD_SERVICE", source.env)
					}
					_, client := newTracedRig(t, tt, []Option{WithCustomTag("side", "server")}, nil)
					tr, agent, err := tracertest.Bootstrap(t, append(source.opts, tracer.WithLogger(testutils.DiscardLogger()))...)
					require.NoError(t, err)
					_, err = callBidi(context.Background(), client, "hello")
					require.NoError(t, err)
					flushUntil(t, tr, agent, 8)
					assert.Nil(t, agent.FindSpan(agenttest.With().Condition("service is not started-svc", func(s *agenttest.Span) bool {
						return s.Service != "started-svc"
					})))
					// The tracer's service is not an override.
					assert.Nil(t, agent.FindSpan(agenttest.With().Condition("has a service source", func(s *agenttest.Span) bool {
						_, ok := s.Meta[ext.KeyServiceSource]
						return ok
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
		})
	}
}

func TestAnalytics(t *testing.T) {
	for _, test := range []struct {
		name string
		env  string
		// global enables analytics globally, which the integration ignores.
		global bool
		want   any
	}{
		{name: "disabled by default"},
		{name: "enabled by the environment", env: "true", want: 1.0},
		{name: "disabled by the environment", env: "false"},
		{name: "not enabled by the global rate", global: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.env != "" {
				t.Setenv("DD_TRACE_CONNECT_ANALYTICS_ENABLED", test.env)
			}
			if test.global {
				testutils.SetGlobalAnalyticsRate(t, 1)
			}
			mt := startMockTracer(t)
			_, client := newTracedRig(t, connectTransport, nil, nil)
			_, err := callUnary(context.Background(), client, "hello")
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
	t.Run("tags set after a span starts beat custom tags", func(t *testing.T) {
		mt := startMockTracer(t)
		custom := []Option{
			WithCustomTag(ext.ResourceName, "custom"),
			WithCustomTag(ext.RPCSystem, "custom"),
			WithCustomTag(ext.NetworkDestinationIP, "custom"),
			WithCustomTag(tagConnectErrorCode, "custom"),
		}
		_, client := newTracedRig(t, connectTransport, custom, custom)
		_, err := callUnary(context.Background(), client, valueNotFound)
		require.Equal(t, connectrpc.CodeNotFound, connectrpc.CodeOf(err))
		clientCall, serverCall := requireCallPair(t, mt, methodKindUnary)
		for _, span := range []*mocktracer.Span{clientCall, serverCall} {
			assert.Equal(t, "custom", span.Tag(ext.ResourceName), span.OperationName())
			assert.Equal(t, "not_found", span.Tag(tagConnectErrorCode), span.OperationName())
		}
		// A client only knows the protocol and the peer once the transport has opened the stream.
		assert.Equal(t, ext.RPCSystemConnectRPC, clientCall.Tag(ext.RPCSystem))
		assert.Equal(t, "127.0.0.1", clientCall.Tag(ext.NetworkDestinationIP))
		assert.Equal(t, "custom", serverCall.Tag(ext.RPCSystem))
		assert.Equal(t, "custom", serverCall.Tag(ext.NetworkDestinationIP))
	})

	for _, test := range []struct {
		name      string
		opts      []Option
		operation string
	}{
		{name: "call span", opts: []Option{WithStreamMessages(false)}, operation: operationServer},
		{name: "message span without a call span", opts: []Option{WithStreamCalls(false)}, operation: operationMessage},
	} {
		t.Run("parent beats the propagated parent/"+test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			propagated, user := tracer.StartSpan("propagated"), tracer.StartSpan("user")
			info := newServerCallInfo(connectrpc.ProtocolNameConnect)
			require.NoError(t, tracer.Inject(propagated.Context(), headerCarrier{header: info.RequestHeader()}))
			opts := append(slices.Clone(test.opts), WithSpanOptions(tracer.ChildOf(user.Context())))
			require.NoError(t, serveFake(context.Background(), bidiSpec, info, &fakeServerStream{}, sendMessage, NewServerInterceptor(opts...)))
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, test.operation, spans[0].OperationName())
			assert.Equal(t, user.Context().SpanID(), spans[0].ParentID())
		})
	}

	// Without call spans, every message span is a local root.
	t.Run("run once per message span without call spans", func(t *testing.T) {
		for _, tt := range testTransports {
			t.Run(tt.name, func(t *testing.T) {
				mt := startMockTracer(t)
				var calls atomic.Int32
				opts := []Option{WithSpanOptions(func(*tracer.StartSpanConfig) { calls.Add(1) }), WithStreamCalls(false)}
				_, client := newTracedRig(t, tt, opts, opts)
				_, err := callBidi(context.Background(), client, "hello")
				require.NoError(t, err)
				// Each side: Send, Receive, and the Receive of the end of the stream.
				spans := waitForSpans(t, mt, 6)
				assert.Len(t, spans, 6)
				for _, span := range spans {
					assert.Equal(t, operationMessage, span.OperationName())
				}
				assert.EqualValues(t, 6, calls.Load())
			})
		}
	})
}

// metadataTestHeader returns request metadata with a key of every kind that WithMetadataTags
// reports or excludes.
func metadataTestHeader() *connectrpc.Header {
	header := new(connectrpc.Header)
	header.SetValues("X-Test", []string{"a", "b"})
	for key, value := range map[string]string{
		"X-Secret":            "hidden",
		"X-Other":             "other",
		"Authorization":       "Bearer secret",
		"Cookie":              "session=secret",
		"Traceparent":         "00-1-2-01",
		"X-Datadog-Trace-Id":  "1",
		"B3":                  "trace-span-1",
		"Payload-Bin":         connectrpc.EncodeBinaryHeader([]byte("binary")),
		"Ot-Baggage-Secret":   "secret",
		"Proxy-Authorization": "secret",
	} {
		header.Set(key, value)
	}
	return header
}

func TestMetadataTags(t *testing.T) {
	// The client reports the metadata set on the CallInfo, which also holds the trace headers it
	// injects; the server reports every request header it received.
	t.Run("end to end", func(t *testing.T) {
		want := map[string]any{"rpc.request.metadata.x-test.0": "a", "rpc.request.metadata.x-test.1": "b"}
		excluded := []string{
			"x-secret", "x-other", "authorization", "cookie", "traceparent", "tracestate", "b3",
			"payload-bin", "ot-baggage-secret", "proxy-authorization", "x-datadog-trace-id", "x-datadog-parent-id",
		}
		for _, tt := range testTransports {
			t.Run(tt.name, func(t *testing.T) {
				mt := startMockTracer(t)
				opts := []Option{WithMetadataTags(), WithIgnoredMetadata("X-SECRET", "x-oThEr")}
				_, client := newTracedRig(t, tt, opts, opts)
				ctx, info := connectrpc.NewClientContext(context.Background())
				for key, values := range metadataTestHeader().All() {
					info.RequestHeader().SetValues(key, values)
				}
				_, err := callUnary(ctx, client, "hello")
				require.NoError(t, err)
				clientCall, serverCall := requireCallPair(t, mt, methodKindUnary)
				assert.Equal(t, want, tagsWithPrefix(clientCall, tagRequestMetadataPrefix))
				serverTags := tagsWithPrefix(serverCall, tagRequestMetadataPrefix)
				if tt.inProcess {
					// The in-process server receives the client's metadata only.
					assert.Equal(t, want, serverTags)
					return
				}
				for key, value := range want {
					assert.Equal(t, value, serverTags[key], key)
				}
				for _, key := range excluded {
					assert.NotContains(t, serverTags, tagRequestMetadataPrefix+key+".0")
				}
			})
		}
	})
}

// TestErrorCheckArguments checks what WithErrorCheck is called with end to end; connectshared
// tests which errors it is called for.
func TestErrorCheckArguments(t *testing.T) {
	type call struct {
		procedure string
		err       error
	}
	t.Run("end to end", func(t *testing.T) {
		for _, tt := range testTransports {
			t.Run(tt.name, func(t *testing.T) {
				mt := startMockTracer(t)
				var mu sync.Mutex
				calls := make(map[string][]call)
				errCheck := func(side string) Option {
					return WithErrorCheck(func(procedure string, err error) bool {
						mu.Lock()
						defer mu.Unlock()
						calls[side] = append(calls[side], call{procedure: procedure, err: err})
						return true
					})
				}
				_, client := newTracedRig(t, tt, []Option{errCheck("server")}, []Option{errCheck("client")})
				_, err := callUnary(context.Background(), client, valueNotFound)
				require.Equal(t, connectrpc.CodeNotFound, connectrpc.CodeOf(err))
				requireCallPair(t, mt, methodKindUnary)
				mu.Lock()
				defer mu.Unlock()
				for _, side := range []string{"client", "server"} {
					require.NotEmpty(t, calls[side], side)
					for _, call := range calls[side] {
						assert.Equal(t, unaryProcedure, call.procedure, side)
						assert.Equal(t, connectrpc.CodeNotFound, connectrpc.CodeOf(call.err), side)
					}
				}
			})
		}
	})
}
