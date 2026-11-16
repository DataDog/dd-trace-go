// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bidiProcedure = "/test.connect.v1.TestService/Bidi"

var bidiSpec = Spec{Procedure: bidiProcedure, StreamType: StreamTypeBidi}

// sides are the two components that an interceptor traces.
var sides = []struct {
	name      string
	component instrumentation.Component
}{{"client", instrumentation.ComponentClient}, {"server", instrumentation.ComponentServer}}

// startAllSpanKinds starts and finishes, on each side, a call span and a message span that is its
// child.
func startAllSpanKinds(ctx context.Context, cfg *Config) {
	for _, side := range sides {
		call, callCtx := cfg.StartCallSpan(ctx, side.component, bidiSpec, &grpcProtocol, nil)
		startMessageSpan(callCtx, cfg, side.component).Finish()
		call.Finish()
	}
}

// startMessageSpan starts a message span of a bidi stream over gRPC on side, as the integrations
// do: with ClientMessages on clients, and with ServerMessages on servers, whose request headers
// propagate no trace.
func startMessageSpan(ctx context.Context, cfg *Config, side instrumentation.Component) *tracer.Span {
	if side == instrumentation.ComponentClient {
		var messages ClientMessages
		messages.Init(ctx, cfg, bidiSpec, &grpcProtocol, PeerTags{})
		return messages.Start()
	}
	var messages ServerMessages
	messages.Init(ctx, cfg, bidiSpec, &grpcProtocol, tracer.TextMapCarrier{})
	span, _ := messages.Start()
	return span
}

// sharedStartTags returns the start tags that cfg's spans share (see startTags), by key.
func sharedStartTags(cfg *Config) map[startKey]map[string]any {
	shared := map[startKey]map[string]any{}
	cfg.starts.starts.Range(func(key, cached any) bool {
		var start tracer.StartSpanConfig
		cached.(*cachedStart).opt(&start)
		shared[key.(startKey)] = start.Tags
		return true
	})
	return shared
}

// userOptions are the configurations with and without the user options that may change a span's
// start tags.
var userOptions = []struct {
	name    string
	setters []func(*Config)
	// resource is the resource that the spans of bidiSpec start with.
	resource string
}{
	{name: "without user options", resource: bidiProcedure},
	{
		name: "with user options",
		setters: []func(*Config){func(c *Config) {
			c.AddCustomTag("custom-tag", "v")
			c.AddSpanOptions(tracer.Tag("span-option", "v"), tracer.ResourceName("from-span-option"))
		}},
		resource: "from-span-option",
	},
}

func TestSpanBases(t *testing.T) {
	bases := func(cfg *Config) map[string]*tracer.StartSpanConfig {
		return map[string]*tracer.StartSpanConfig{
			"client call":    cfg.client.call,
			"client message": cfg.client.message,
			"server call":    cfg.server.call,
			"server message": cfg.server.message,
		}
	}

	for _, test := range []struct {
		name      string
		analytics bool
	}{
		{name: "hold only static tags"},
		{name: "hold the analytics rate", analytics: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.analytics {
				t.Setenv("DD_TRACE_CONNECT_ANALYTICS_ENABLED", "true")
			}
			for _, pkg := range testPackages {
				// Per-span and user-provided values must stay out of the shared bases.
				cfg := newTestConfigFor(pkg, func(c *Config) {
					c.SetService("svc")
					c.AddCustomTag("custom-tag", "v")
					c.AddSpanOptions(tracer.Tag("span-option", "v"))
				})
				static := map[string]any{ext.SpanType: "rpc", ext.Component: string(pkg)}
				want := map[string]map[string]any{
					"client call":    {ext.SpanKind: "client", "_dd.measured": 1},
					"client message": {},
					"server call":    {ext.SpanKind: "server", "_dd.measured": 1},
					"server message": {"_dd.measured": 1},
				}
				for name, base := range bases(cfg) {
					wantTags := maps.Clone(static)
					maps.Copy(wantTags, want[name])
					if test.analytics {
						wantTags[ext.EventSampleRate] = 1.0
					}
					name = string(pkg) + " " + name
					assert.Equal(t, wantTags, base.Tags, name)
					// WithStartSpanConfig copies these fields into every span it starts.
					assert.Nil(t, base.Parent, name)
					assert.Nil(t, base.SpanLinks, name)
					assert.Nil(t, base.Context, name)
					assert.Zero(t, base.SpanID, name)
					assert.True(t, base.StartTime.IsZero(), name)
				}
			}
		})
	}

	// Nor are the start tags that spans share (see startTags).
	t.Run("are not mutated by spans", func(t *testing.T) {
		for _, options := range userOptions {
			t.Run(options.name, func(t *testing.T) {
				mt := startMockTracer(t)
				cfg := newTestConfig(options.setters...)
				before := map[string]map[string]any{}
				for name, base := range bases(cfg) {
					before[name] = maps.Clone(base.Tags)
				}
				startAllSpanKinds(context.Background(), cfg)
				sharedBefore := map[startKey]map[string]any{}
				for key, tags := range sharedStartTags(cfg) {
					sharedBefore[key] = maps.Clone(tags)
				}
				// A call span and a message span on each side, unless user options may change them.
				if len(options.setters) == 0 {
					require.Len(t, sharedBefore, 4)
				} else {
					require.Empty(t, sharedBefore)
				}
				for range 3 {
					for _, side := range sides {
						call, callCtx := cfg.StartCallSpan(context.Background(), side.component, bidiSpec, &grpcProtocol, nil)
						message := startMessageSpan(callCtx, cfg, side.component)
						for _, span := range []*tracer.Span{call, message} {
							span.SetTag("per-span", "x")
							span.SetTag(ext.ResourceName, "per-span")
							span.Finish()
						}
					}
				}
				for name, base := range bases(cfg) {
					assert.Equal(t, before[name], base.Tags, name)
				}
				assert.Equal(t, sharedBefore, sharedStartTags(cfg))
				require.Len(t, mt.FinishedSpans(), 16)
			})
		}
	})

	t.Run("are safe for concurrent use", func(t *testing.T) {
		for _, options := range userOptions {
			t.Run(options.name, func(t *testing.T) {
				mt := startMockTracer(t)
				cfg := newTestConfig(options.setters...)
				var wg sync.WaitGroup
				for range 8 {
					wg.Go(func() {
						for range 50 {
							startAllSpanKinds(context.Background(), cfg)
						}
					})
				}
				wg.Wait()
				assert.Len(t, mt.FinishedSpans(), 8*50*4)
			})
		}
	})

	// A stream's Send and Receive may run concurrently and share the stream's message tags.
	t.Run("share message tags safely", func(t *testing.T) {
		for _, options := range userOptions {
			t.Run(options.name, func(t *testing.T) {
				mt := startMockTracer(t)
				cfg := newTestConfig(options.setters...)
				var messages ClientMessages
				messages.Init(context.Background(), cfg, bidiSpec, &connectProtocol, PeerTags{})
				var wg sync.WaitGroup
				for range 8 {
					wg.Go(func() {
						for range 100 {
							span := messages.Start()
							span.SetTag("per-span", "x")
							span.SetTag(ext.ResourceName, "per-span")
							span.Finish()
						}
					})
				}
				wg.Wait()
				spans := mt.FinishedSpans()
				require.Len(t, spans, 800)
				for _, span := range spans {
					assert.Equal(t, "per-span", span.Tag(ext.ResourceName))
					assert.Equal(t, "Bidi", span.Tag(ext.RPCMethod))
					assert.Equal(t, "connectrpc", span.Tag(ext.RPCSystem))
				}
				// Spans that start later have the stream's tags.
				span := messages.Start()
				span.Finish()
				assert.Equal(t, options.resource, mt.FinishedSpans()[800].Tag(ext.ResourceName))
			})
		}
	})

	// The tracer drops _dd.measured from top-level spans, so the parent has the same service.
	t.Run("measure call spans and server message spans", func(t *testing.T) {
		mt := startMockTracer(t)
		parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent", tracer.ServiceName("svc"))
		startAllSpanKinds(ctx, newTestConfig(func(c *Config) { c.SetService("svc") }))
		parent.Finish()
		spans := mt.FinishedSpans()
		require.Len(t, spans, 5)
		clientCall := spansNamed(spans, operationClient)[0]
		for _, span := range spans {
			switch {
			case span.OperationName() == "parent":
			case span.ParentID() == clientCall.SpanID():
				assert.Nil(t, span.Tag("_dd.measured"), "client message")
			default:
				assert.EqualValues(t, 1, span.Tag("_dd.measured"), span.OperationName())
				assert.Nil(t, span.Tag("_dd.top_level"), span.OperationName())
			}
		}
	})
}

func TestStartCallSpan(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol *Protocol
		want     map[string]any // a nil value means the tag is absent
	}{
		{
			name: "with a protocol", protocol: &grpcProtocol,
			want: map[string]any{ext.RPCSystem: "grpc", ext.GRPCFullMethod: bidiProcedure},
		},
		// A client knows the protocol only once the transport has opened the stream.
		{
			name: "before the protocol is known",
			want: map[string]any{ext.RPCSystem: nil, ext.GRPCFullMethod: nil},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, side := range sides {
				mt := startMockTracer(t)
				span, ctx := newTestConfig().StartCallSpan(context.Background(), side.component, bidiSpec, test.protocol, nil)
				active, ok := tracer.SpanFromContext(ctx)
				require.True(t, ok)
				assert.Same(t, span, active)
				span.Finish()
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				name := side.name
				assert.Equal(t, bidiProcedure, spans[0].Tag(ext.ResourceName), name)
				assert.Equal(t, "test.connect.v1.TestService", spans[0].Tag(ext.RPCService), name)
				assert.Equal(t, "Bidi", spans[0].Tag(ext.RPCMethod), name)
				assert.Equal(t, "bidi_streaming", spans[0].Tag("connect.method.kind"), name)
				for key, value := range test.want {
					assert.Equal(t, value, spans[0].Tag(key), name, key)
				}
			}
		})
	}
}

func TestStartMessageSpan(t *testing.T) {
	for _, test := range []struct {
		name string
		// start starts a message span on side, of a client stream whose peer has the tags peer.
		start func(cfg *Config, side instrumentation.Component, peer PeerTags) *tracer.Span
	}{
		{
			name: "startTaggedMessageSpan",
			start: func(cfg *Config, side instrumentation.Component, peer PeerTags) *tracer.Span {
				return cfg.startTaggedMessageSpan(context.Background(), cfg.messageTags(bidiSpec, &connectProtocol), side, peer)
			},
		},
		{
			name: "ClientMessages and ServerMessages",
			start: func(cfg *Config, side instrumentation.Component, peer PeerTags) *tracer.Span {
				if side == instrumentation.ComponentClient {
					var messages ClientMessages
					messages.Init(context.Background(), cfg, bidiSpec, &connectProtocol, peer)
					return messages.Start()
				}
				var messages ServerMessages
				messages.Init(context.Background(), cfg, bidiSpec, &connectProtocol, nil)
				span, _ := messages.Start()
				return span
			},
		},
	} {
		t.Run("sets the peer tags/"+test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			test.start(newTestConfig(), instrumentation.ComponentClient, NewPeerTags("127.0.0.1:8080")).Finish()
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, operationMessage, spans[0].OperationName())
			assert.Equal(t, "127.0.0.1", spans[0].Tag(ext.NetworkDestinationIP))
			assert.Equal(t, 8080.0, spans[0].Tag(ext.NetworkDestinationPort))
		})

		// Without a parent, a message span is a local root, which must have a span kind.
		t.Run("sets the span kind of a local root/"+test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			cfg := newTestConfig()
			for _, side := range sides {
				test.start(cfg, side.component, PeerTags{}).Finish()
			}
			spans := mt.FinishedSpans()
			require.Len(t, spans, 2)
			assert.Equal(t, ext.SpanKindClient, spans[0].Tag(ext.SpanKind))
			assert.Equal(t, ext.SpanKindServer, spans[1].Tag(ext.SpanKind))
		})
	}
}

// TestStartTags checks which spans share their start tags, and that these are the tags that the
// spans would otherwise build for themselves.
func TestStartTags(t *testing.T) {
	// startSpans starts and finishes, on each side, a call span of spec over protocol, and unless
	// protocol is nil, a message span that is its child.
	startSpans := func(cfg *Config, spec Spec, protocol *Protocol) {
		for _, side := range sides {
			call, ctx := cfg.StartCallSpan(context.Background(), side.component, spec, protocol, nil)
			if protocol != nil {
				var messages messageSpans
				messages.init(ctx, cfg, side.component, spec, protocol)
				messages.start(PeerTags{}).Finish()
			}
			call.Finish()
		}
	}

	for _, test := range []struct {
		name    string
		setters []func(*Config)
		spec    Spec
		shared  bool
	}{
		{name: "are shared by default", spec: bidiSpec, shared: true},
		{name: "are not shared with span options", setters: []func(*Config){func(c *Config) { c.AddSpanOptions(tracer.Tag("span-option", "v")) }}, spec: bidiSpec},
		{name: "are not shared with custom tags", setters: []func(*Config){func(c *Config) { c.AddCustomTag("custom-tag", "v") }}, spec: bidiSpec},
		{
			name: "are not shared by the procedures that the client chose",
			spec: Spec{Procedure: bidiProcedure, StreamType: StreamTypeBidi, Unrecognized: true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			cfg := newTestConfig(test.setters...)
			assert.Equal(t, test.shared, cfg.sharesStartTags(test.spec))
			startSpans(cfg, test.spec, &grpcProtocol)
			startSpans(cfg, test.spec, &grpcProtocol)
			want := 0
			if test.shared {
				// A call span and a message span on each side.
				want = 4
			}
			assert.Len(t, sharedStartTags(cfg), want)
			require.Len(t, mt.FinishedSpans(), 8)
		})
	}

	// A span option that changes nothing makes the spans build their own start tags.
	t.Run("are the tags that spans build otherwise", func(t *testing.T) {
		buildOwn := func(c *Config) { c.AddSpanOptions(func(*tracer.StartSpanConfig) {}) }
		for _, service := range []struct {
			name    string
			setters []func(*Config)
		}{
			{name: "default service"},
			{name: "WithService", setters: []func(*Config){func(c *Config) { c.SetService("svc") }}},
		} {
			for _, protocol := range []*Protocol{nil, &connectProtocol, &grpcProtocol, &unknownProtocol, &inProcessProtocol} {
				for _, spec := range []Spec{bidiSpec, {Procedure: unaryProcedure}} {
					mt := startMockTracer(t)
					testutils.SetGlobalServiceName(t, "global-svc")
					startSpans(newTestConfig(service.setters...), spec, protocol)
					shared := mt.FinishedSpans()
					mt.Reset()
					startSpans(newTestConfig(append(slices.Clone(service.setters), buildOwn)...), spec, protocol)
					own := mt.FinishedSpans()
					require.Len(t, own, len(shared))
					for i := range own {
						name := fmt.Sprintf("%s/%v/%s: %s", service.name, protocol, spec.Procedure, own[i].OperationName())
						assert.Equal(t, own[i].OperationName(), shared[i].OperationName(), name)
						assert.Equal(t, startTagsOf(own[i]), startTagsOf(shared[i]), name)
					}
				}
			}
		}
	})

	t.Run("follow the default service", func(t *testing.T) {
		startMockTracer(t)
		cfg := newTestConfig()
		for _, service := range []string{"global-svc", "other-svc"} {
			testutils.SetGlobalServiceName(t, service)
			startSpans(cfg, bidiSpec, &grpcProtocol)
			shared := sharedStartTags(cfg)
			// The options of the previous service are replaced.
			assert.Len(t, shared, 4)
			for _, tags := range shared {
				assert.Equal(t, service, tags[ext.KeyServiceSource].(instrumentation.ServiceOverride).Name)
			}
		}
	})

	t.Run("are bounded", func(t *testing.T) {
		mt := startMockTracer(t)
		cfg := newTestConfig()
		for i := range maxCachedStarts + 1 {
			spec := Spec{Procedure: fmt.Sprintf("/test.Service/Method%d", i)}
			span, _ := cfg.StartCallSpan(context.Background(), instrumentation.ComponentClient, spec, &grpcProtocol, nil)
			span.Finish()
		}
		assert.Len(t, sharedStartTags(cfg), maxCachedStarts)
		assert.EqualValues(t, maxCachedStarts, cfg.starts.size.Load())
		spans := mt.FinishedSpans()
		require.Len(t, spans, maxCachedStarts+1)
		assert.Equal(t, fmt.Sprintf("/test.Service/Method%d", maxCachedStarts), spans[maxCachedStarts].Tag(ext.ResourceName))
		assert.Equal(t, fmt.Sprintf("Method%d", maxCachedStarts), spans[maxCachedStarts].Tag(ext.RPCMethod))
	})

	// The spans of a key that is cached build no tags: they cost no more than starting a span with
	// the cached option.
	t.Run("cost a cached span nothing more", func(t *testing.T) {
		startMockTracer(t)
		cfg := newTestConfig()
		for _, side := range sides {
			bases := cfg.bases(side.component)
			callOpt, ok := cfg.startTags(bases.call, bidiSpec, &grpcProtocol)
			require.True(t, ok)
			messageOpt, ok := cfg.startTags(bases.message, bidiSpec, &grpcProtocol)
			require.True(t, ok)
			startedWith := testing.AllocsPerRun(100, func() {
				var buf [8]tracer.StartSpanOption
				span, ctx := tracer.StartSpanFromContext(context.Background(), bases.callOperation, append(buf[:0], callOpt)...)
				_ = cfg.markHandled(ctx, side.component, callMarker{procedure: bidiSpec.Procedure, span: span, traced: true})
				span.Finish()
			})
			call := testing.AllocsPerRun(100, func() {
				span, _ := cfg.StartCallSpan(context.Background(), side.component, bidiSpec, &grpcProtocol, nil)
				span.Finish()
			})
			assert.LessOrEqual(t, call, startedWith, "%s call span", side.name)

			var messages messageSpans
			messages.init(context.Background(), cfg, side.component, bidiSpec, &grpcProtocol)
			startedWith = testing.AllocsPerRun(100, func() {
				var buf [8]tracer.StartSpanOption
				cfg.startMessageSpan(context.Background(), side.component, PeerTags{}, append(buf[:0], messageOpt)).Finish()
			})
			message := testing.AllocsPerRun(100, func() { messages.start(PeerTags{}).Finish() })
			assert.LessOrEqual(t, message, startedWith, "%s message span", side.name)
		}
	})

	// Once the cache is full, the spans of a key that it doesn't hold build their tags as spans that
	// don't share them do: a call span its own, and the message spans of a stream the stream's.
	t.Run("past the bound, cost no more than spans that build their own tags", func(t *testing.T) {
		startMockTracer(t)
		for _, side := range sides {
			cfg := newTestConfig()
			cfg.starts.size.Store(maxCachedStarts)
			bases := cfg.bases(side.component)
			ownTags := testing.AllocsPerRun(100, func() {
				var buf [8]tracer.StartSpanOption
				opts := cfg.appendSpanOptions(buf[:0], callTags(dynamicTags(bidiSpec, &grpcProtocol)), nil, bases.callOpt, cfg.resolveService().opt)
				span, ctx := tracer.StartSpanFromContext(context.Background(), bases.callOperation, opts...)
				_ = cfg.markHandled(ctx, side.component, callMarker{procedure: bidiSpec.Procedure, span: span, traced: true})
				span.Finish()
			})
			call := testing.AllocsPerRun(100, func() {
				span, _ := cfg.StartCallSpan(context.Background(), side.component, bidiSpec, &grpcProtocol, nil)
				span.Finish()
			})
			assert.LessOrEqual(t, call, ownTags, "%s call span", side.name)

			var messages messageSpans
			messages.init(context.Background(), cfg, side.component, bidiSpec, &grpcProtocol)
			streamTags := cfg.messageTags(bidiSpec, &grpcProtocol)
			ownTags = testing.AllocsPerRun(100, func() {
				cfg.startTaggedMessageSpan(context.Background(), streamTags, side.component, PeerTags{}).Finish()
			})
			message := testing.AllocsPerRun(100, func() { messages.start(PeerTags{}).Finish() })
			assert.LessOrEqual(t, message, ownTags, "%s message span", side.name)
			assert.Empty(t, sharedStartTags(cfg))
		}
	})

	// The cache may fill up once a stream has started, before its message spans do.
	t.Run("past the bound, the tags of a started stream's message spans", func(t *testing.T) {
		mt := startMockTracer(t)
		for _, side := range sides {
			cfg := newTestConfig()
			var messages messageSpans
			messages.init(context.Background(), cfg, side.component, bidiSpec, &grpcProtocol)
			cfg.starts.size.Store(maxCachedStarts)
			messages.start(PeerTags{}).Finish()
			assert.Empty(t, sharedStartTags(cfg))
		}
		own := mt.FinishedSpans()
		mt.Reset()
		for _, side := range sides {
			startMessageSpan(context.Background(), newTestConfig(), side.component).Finish()
		}
		shared := mt.FinishedSpans()
		require.Len(t, own, len(shared))
		for i := range own {
			assert.Equal(t, startTagsOf(shared[i]), startTagsOf(own[i]), sides[i].name)
		}
	})

	// Spans that start concurrently may each build the option of a key, and fill the cache.
	t.Run("cache nothing once full", func(t *testing.T) {
		var cache startCache
		cache.size.Store(maxCachedStarts)
		cache.store(startKey{spec: bidiSpec}, &cachedStart{}, false)
		_, ok := cache.load(startKey{spec: bidiSpec})
		assert.False(t, ok)
		assert.EqualValues(t, maxCachedStarts, cache.size.Load())
	})
	for _, test := range []struct {
		name    string
		replace bool
	}{
		{name: "keep the option cached first"},
		{name: "replace the option of another service", replace: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := startKey{spec: bidiSpec}
			first, second := &cachedStart{}, &cachedStart{}
			var cache startCache
			cache.store(key, first, false)
			cache.store(key, second, test.replace)
			cached, ok := cache.load(key)
			require.True(t, ok)
			want := first
			if test.replace {
				want = second
			}
			assert.Same(t, want, cached)
			assert.EqualValues(t, 1, cache.size.Load())
		})
	}
}

// startTagsOf returns the tags of span, without the upper trace ID bits, which depend on the time
// when the trace started.
func startTagsOf(span *mocktracer.Span) map[string]any {
	tags := span.Tags()
	delete(tags, "_dd.p.tid")
	return tags
}

// TestCallTags checks that callTags hands its map to the span config, which a later base merges
// into, and keeps the tags of an earlier option.
func TestCallTags(t *testing.T) {
	for _, test := range []struct {
		name string
		// earlier are the tags of an option applied before callTags, and base those of a base
		// config applied after it.
		earlier, base map[string]any
		want          map[string]any
		// wantCallTags is the map given to callTags afterwards.
		wantCallTags map[string]any
	}{
		{
			name: "installs the map", base: map[string]any{"base": "tag"},
			want: map[string]any{"call": "tag", "base": "tag"}, wantCallTags: map[string]any{"call": "tag", "base": "tag"},
		},
		{
			name: "merges into earlier tags", earlier: map[string]any{"earlier": "tag"},
			want: map[string]any{"earlier": "tag", "call": "tag"}, wantCallTags: map[string]any{"call": "tag"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tags := map[string]any{"call": "tag"}
			cfg := tracer.StartSpanConfig{Tags: test.earlier}
			callTags(tags)(&cfg)
			if test.base != nil {
				tracer.WithStartSpanConfig(&tracer.StartSpanConfig{Tags: test.base})(&cfg)
			}
			assert.Equal(t, test.want, cfg.Tags)
			assert.Equal(t, test.wantCallTags, tags)
		})
	}
}

func TestSpanOptions(t *testing.T) {
	t.Run("precedence", func(t *testing.T) {
		mt := startMockTracer(t)
		cfg := newTestConfig(func(c *Config) {
			c.AddSpanOptions(
				tracer.Tag(ext.RPCMethod, "from-span-option"),
				tracer.Tag("both", "from-span-option"),
				tracer.SpanType("from-span-option"),
			)
			c.AddCustomTag(ext.ResourceName, "from-custom-tag")
			c.AddCustomTag("both", "from-custom-tag")
		})
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

	t.Run("preferUserService", func(t *testing.T) {
		override := instrumentation.ServiceOverride{Name: "opt-svc", Source: instrumentation.ServiceSourceWithServiceOption}
		for _, test := range []struct {
			name       string
			tags, want map[string]any
		}{
			{
				name: "a user service drops the interceptor's",
				tags: map[string]any{ext.ServiceName: "user-svc", ext.KeyServiceSource: override},
				want: map[string]any{ext.ServiceName: "user-svc"},
			},
			{
				name: "the interceptor's service is kept otherwise",
				tags: map[string]any{ext.KeyServiceSource: override, "other": "tag"},
				want: map[string]any{ext.KeyServiceSource: override, "other": "tag"},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				cfg := &tracer.StartSpanConfig{Tags: test.tags}
				preferUserService(cfg)
				assert.Equal(t, test.want, cfg.Tags)
			})
		}
	})

	// The tracer applies start tags in random map order, so a lost race shows over many spans.
	for _, test := range []struct {
		name    string
		setters []func(*Config)
	}{
		{name: "WithSpanOptions", setters: []func(*Config){func(c *Config) { c.AddSpanOptions(tracer.ServiceName("user-svc")) }}},
		{
			name: "WithSpanOptions and WithService",
			setters: []func(*Config){
				func(c *Config) { c.SetService("opt-svc") },
				func(c *Config) { c.AddSpanOptions(tracer.ServiceName("user-svc")) },
			},
		},
		{
			name: "WithCustomTag and WithService",
			setters: []func(*Config){
				func(c *Config) { c.SetService("opt-svc") },
				func(c *Config) { c.AddCustomTag(ext.ServiceName, "user-svc") },
			},
		},
	} {
		t.Run("user service beats the interceptor's/"+test.name, func(t *testing.T) {
			testutils.SetGlobalServiceName(t, "global-svc")
			mt := startMockTracer(t)
			cfg := newTestConfig(test.setters...)
			for range 50 {
				startAllSpanKinds(context.Background(), cfg)
			}
			spans := mt.FinishedSpans()
			require.Len(t, spans, 200)
			interceptorSources := []any{instrumentation.ServiceSourceWithServiceOption, string(instrumentation.PackageConnectRPC)}
			for _, span := range spans {
				require.Equal(t, "user-svc", span.Tag(ext.ServiceName), span.OperationName())
				require.NotContains(t, interceptorSources, span.Tag(ext.KeyServiceSource), span.OperationName())
			}
		})
	}

	// Span options may be stateful, so they must not run to build the configuration or to check
	// whether a span is a local root.
	t.Run("run once per span", func(t *testing.T) {
		mt := startMockTracer(t)
		var calls atomic.Int32
		cfg := newTestConfig(func(c *Config) {
			c.AddSpanOptions(func(*tracer.StartSpanConfig) { calls.Add(1) })
		})
		require.Zero(t, calls.Load(), "Init ran a span option")
		for range 5 {
			startAllSpanKinds(context.Background(), cfg)
		}
		assert.EqualValues(t, 20, calls.Load())
		assert.Len(t, mt.FinishedSpans(), 20)
	})
}
