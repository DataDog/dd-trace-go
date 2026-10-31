// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"cmp"
	"context"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	unaryProcedure = "/test.connect.v1.TestService/Unary"
	logPrefix      = "contrib/connectrpc.com/connect"
)

// testPackages are the integrations that use this package.
var testPackages = []instrumentation.Package{instrumentation.PackageConnectRPC, instrumentation.PackageConnectRPCV2}

var testInstr = map[instrumentation.Package]*instrumentation.Instrumentation{
	instrumentation.PackageConnectRPC:   instrumentation.Load(instrumentation.PackageConnectRPC),
	instrumentation.PackageConnectRPCV2: instrumentation.Load(instrumentation.PackageConnectRPCV2),
}

// newTestConfigFor returns the configuration of an interceptor of pkg with setters applied, as an
// integration builds it from its options.
func newTestConfigFor(pkg instrumentation.Package, setters ...func(*Config)) *Config {
	cfg := NewConfig(testInstr[pkg], pkg, &testLibrary)
	for _, set := range setters {
		set(cfg)
	}
	cfg.Init()
	return cfg
}

// newTestConfig is newTestConfigFor with connectrpc.com/connect.
func newTestConfig(setters ...func(*Config)) *Config {
	return newTestConfigFor(instrumentation.PackageConnectRPC, setters...)
}

// testCode is a connect Code of another major version.
type testCode uint32

func TestConfig(t *testing.T) {
	for _, test := range []struct {
		name    string
		setters []func(*Config)
		check   func(*testing.T, *Config)
	}{
		{
			name: "defaults",
			check: func(t *testing.T, cfg *Config) {
				assert.Nil(t, cfg.service.Load())
				assert.Equal(t, map[Code]bool{CodeCanceled: true}, cfg.nonErrorCodes)
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
				assert.Nil(t, cfg.tags)
				assert.Nil(t, cfg.customTags)
				assert.ElementsMatch(t, []string{
					"authorization", "baggage", "b3", "cookie", "proxy-authorization", "set-cookie",
					"traceparent", "tracestate", "x-api-key", "x-auth-token", "x-b3-flags",
					"x-b3-parentspanid", "x-b3-sampled", "x-b3-spanid", "x-b3-traceid", "x-datadog-origin",
					"x-datadog-parent-id", "x-datadog-sampling-priority", "x-datadog-tags", "x-datadog-trace-id",
				}, slices.Collect(maps.Keys(cfg.ignoredMetadata)))
				assert.Equal(t, instrumentation.PackageConnectRPC, cfg.pkg)
				assert.Equal(t, logPrefix, cfg.logPrefix)
				assert.Same(t, &testLibrary, cfg.lib)
			},
		},
		{
			name: "WithService", setters: []func(*Config){func(c *Config) { c.SetService("svc") }},
			check: func(t *testing.T, cfg *Config) {
				require.NotNil(t, cfg.service.Load())
				assert.Equal(t, "svc", cfg.service.Load().name)
				assert.Same(t, cfg.service.Load(), cfg.resolveService())
			},
		},
		{
			name: "WithStreamCalls and WithStreamMessages",
			setters: []func(*Config){
				func(c *Config) { c.SetStreamCalls(false) },
				func(c *Config) { c.SetStreamMessages(false) },
			},
			check: func(t *testing.T, cfg *Config) {
				assert.False(t, cfg.traceStreamCalls)
				assert.False(t, cfg.traceStreamMessages)
			},
		},
		{
			name: "NoDebugStack", setters: []func(*Config){(*Config).SetNoDebugStack},
			check: func(t *testing.T, cfg *Config) { assert.True(t, cfg.noDebugStack) },
		},
		{
			name: "NonErrorCodes replaces the defaults",
			setters: []func(*Config){func(c *Config) {
				SetNonErrorCodes(c, []testCode{5, 10})
			}},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, map[Code]bool{5: true, 10: true}, cfg.nonErrorCodes)
			},
		},
		{
			name: "NonErrorCodes without codes", setters: []func(*Config){func(c *Config) { SetNonErrorCodes[Code](c, nil) }},
			check: func(t *testing.T, cfg *Config) { assert.Empty(t, cfg.nonErrorCodes) },
		},
		{
			name: "WithErrorCheck", setters: []func(*Config){func(c *Config) { c.SetErrorCheck(func(string, error) bool { return false }) }},
			check: func(t *testing.T, cfg *Config) { assert.NotNil(t, cfg.errCheck) },
		},
		{
			name: "WithUntracedMethods", setters: []func(*Config){func(c *Config) { c.SetUntracedMethods(MethodSet(unaryProcedure, bidiProcedure)) }},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, map[string]struct{}{unaryProcedure: {}, bidiProcedure: {}}, cfg.untracedMethods)
			},
		},
		{
			name: "WithUntracedMethods replaces earlier calls",
			setters: []func(*Config){
				func(c *Config) { c.SetUntracedMethods(MethodSet(unaryProcedure)) },
				func(c *Config) { c.SetUntracedMethods(MethodSet(bidiProcedure)) },
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, map[string]struct{}{bidiProcedure: {}}, cfg.untracedMethods)
			},
		},
		{
			name:    "WithIgnoredMetadata lowercases keys",
			setters: []func(*Config){(*Config).EnableMetadataTags, func(c *Config) { c.IgnoreMetadata("X-Secret") }},
			check: func(t *testing.T, cfg *Config) {
				assert.True(t, cfg.withMetadataTags)
				assert.Contains(t, cfg.ignoredMetadata, "x-secret")
				assert.Contains(t, cfg.ignoredMetadata, "authorization", "the defaults are kept")
			},
		},
		{
			name:    "WithRequestTags and WithErrorDetailTags",
			setters: []func(*Config){(*Config).EnableRequestTags, (*Config).EnableErrorDetailTags},
			check: func(t *testing.T, cfg *Config) {
				assert.True(t, cfg.withRequestTags)
				assert.True(t, cfg.withErrorDetailTags)
			},
		},
		{
			name: "WithCustomTag",
			setters: []func(*Config){
				func(c *Config) { c.AddCustomTag("a", 1) },
				func(c *Config) { c.AddCustomTag("b", "two") },
				func(c *Config) { c.AddCustomTag("a", "one") },
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, map[string]any{"a": "one", "b": "two"}, cfg.tags)
				assert.NotNil(t, cfg.customTags)
			},
		},
		{
			name: "WithSpanOptions appends",
			setters: []func(*Config){
				func(c *Config) { c.AddSpanOptions(tracer.Tag("a", 1)) },
				func(c *Config) { c.AddSpanOptions(tracer.Tag("b", 2), tracer.Tag("c", 3)) },
			},
			check: func(t *testing.T, cfg *Config) { assert.Len(t, cfg.spanOpts, 3) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.check(t, newTestConfig(test.setters...))
		})
	}
}

// TestConfigAccessors checks that each accessor reports the setting it reads.
func TestConfigAccessors(t *testing.T) {
	cfg := newTestConfig()
	assert.True(t, cfg.StreamCalls())
	assert.True(t, cfg.StreamMessages())
	assert.False(t, cfg.MetadataTags())
	assert.False(t, cfg.RequestTags())

	cfg = newTestConfig(func(c *Config) {
		c.SetStreamCalls(false)
		c.SetStreamMessages(false)
		c.EnableMetadataTags()
		c.EnableRequestTags()
	})
	assert.False(t, cfg.StreamCalls())
	assert.False(t, cfg.StreamMessages())
	assert.True(t, cfg.MetadataTags())
	assert.True(t, cfg.RequestTags())
}

func TestService(t *testing.T) {
	t.Run("WithService", func(t *testing.T) {
		mt := startMockTracer(t)
		startAllSpanKinds(context.Background(), newTestConfig(func(c *Config) { c.SetService("svc") }))
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
		// services are the global services that spans start with, in turn.
		services []string
	}{
		{name: "defaults to the global service", services: []string{"global-svc"}},
		{name: "default is resolved when spans start", configFirst: true, services: []string{"global-svc"}},
		{name: "spans follow a changed default", configFirst: true, services: []string{"global-svc", "other-svc", "global-svc"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			var cfg *Config
			if test.configFirst {
				cfg = newTestConfig()
			}
			testutils.SetGlobalServiceName(t, test.services[0])
			if !test.configFirst {
				cfg = newTestConfig()
			}
			for _, service := range test.services {
				testutils.SetGlobalServiceName(t, service)
				startAllSpanKinds(context.Background(), cfg)
				spans := mt.FinishedSpans()
				require.Len(t, spans, 4)
				for _, span := range spans {
					assert.Equal(t, service, span.Tag(ext.ServiceName), span.OperationName())
					// No service source is reported for the global service.
					assert.Nil(t, span.Tag(ext.KeyServiceSource), span.OperationName())
				}
				mt.Reset()
			}
			assert.Nil(t, cfg.service.Load(), "cached before the tracer started")
		})
	}

	// Until then, spans don't allocate the default service while its name is unchanged.
	t.Run("default is reused while unchanged", func(t *testing.T) {
		startMockTracer(t)
		testutils.SetGlobalServiceName(t, "global-svc")
		cfg := newTestConfig()
		first := cfg.resolveService()
		assert.Same(t, first, cfg.resolveService())
		assert.Zero(t, testing.AllocsPerRun(100, func() { cfg.resolveService() }))
		testutils.SetGlobalServiceName(t, "other-svc")
		assert.Equal(t, "other-svc", cfg.resolveService().name)
		assert.Nil(t, cfg.service.Load(), "cached before the tracer started")
	})

	// Every span gets the interceptor's service, so that none inherits the service of a local
	// parent such as an HTTP server span. A span is top-level, the entry point of its service, if it
	// has no local parent or its final service differs from its parent's; the tracer then drops its
	// _dd.measured. This needs the real tracer: the mock tracer neither defaults nor maps services.
	binaryService := filepath.Base(os.Args[0])
	for _, test := range []struct {
		name    string
		env     map[string]string
		setters []func(*Config)
		// service and source are those of every connect span; an empty source means none.
		service, source string
		// tracerService is the service of a span started without one.
		tracerService string
	}{
		{name: "no DD_SERVICE", service: binaryService, tracerService: binaryService},
		// User span options and custom tags make the spans build their own start options.
		{
			name: "no DD_SERVICE with a span option", setters: []func(*Config){func(c *Config) { c.AddSpanOptions(tracer.Tag("user.tag", "1")) }},
			service: binaryService, tracerService: binaryService,
		},
		{
			name: "no DD_SERVICE with a custom tag", setters: []func(*Config){func(c *Config) { c.AddCustomTag("user.tag", "1") }},
			service: binaryService, tracerService: binaryService,
		},
		{name: "DD_SERVICE", env: map[string]string{"DD_SERVICE": "dd-svc"}, service: "dd-svc", tracerService: "dd-svc"},
		{
			name: "WithService", setters: []func(*Config){func(c *Config) { c.SetService("opt-svc") }},
			service: "opt-svc", source: instrumentation.ServiceSourceWithServiceOption, tracerService: binaryService,
		},
		{
			name: "user service", setters: []func(*Config){func(c *Config) { c.AddSpanOptions(tracer.ServiceName("user-svc")) }},
			service: "user-svc", source: "m", tracerService: binaryService,
		},
		{
			name: "DD_SERVICE_MAPPING of the default service", env: map[string]string{"DD_SERVICE_MAPPING": binaryService + ":mapped"},
			service: "mapped", source: ext.ServiceSourceMapping, tracerService: "mapped",
		},
		{
			name: "DD_SERVICE_MAPPING of DD_SERVICE", env: map[string]string{"DD_SERVICE": "dd-svc", "DD_SERVICE_MAPPING": "dd-svc:mapped"},
			service: "mapped", source: ext.ServiceSourceMapping, tracerService: "mapped",
		},
		// The tracer maps every span's service, an inherited one too: inheriting "mid" gives "end".
		{
			name: "chained DD_SERVICE_MAPPING", env: map[string]string{"DD_SERVICE_MAPPING": binaryService + ":mid,mid:end"},
			service: "mid", source: ext.ServiceSourceMapping, tracerService: "mid",
		},
	} {
		for _, parent := range []struct {
			name string
			none bool
			opts []tracer.StartSpanOption
			// service is the parent's service if it is not the tracerService.
			service string
		}{
			{name: "without a parent", none: true},
			{
				name: "under an HTTP server span", service: "http.router",
				opts: []tracer.StartSpanOption{tracer.ServiceName("http.router"), tracer.SpanType(ext.SpanTypeWeb), tracer.Tag(ext.SpanKind, ext.SpanKindServer)},
			},
			{name: "under a span of the tracer's service"},
		} {
			for _, calls := range []struct {
				name    string
				enabled bool
			}{{"with stream calls", true}, {"without stream calls", false}} {
				t.Run("top-level spans/"+test.name+"/"+parent.name+"/"+calls.name, func(t *testing.T) {
					testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
					for key, value := range test.env {
						t.Setenv(key, value)
					}
					tr, agent, err := tracertest.Bootstrap(t, tracer.WithLogger(testutils.DiscardLogger()))
					require.NoError(t, err)
					cfg := newTestConfig(append(slices.Clone(test.setters), func(c *Config) { c.SetStreamCalls(calls.enabled) })...)
					ctx := context.Background()
					var started []*tracer.Span
					var parentID uint64
					if !parent.none {
						var span *tracer.Span
						span, ctx = tracer.StartSpanFromContext(ctx, "parent", parent.opts...)
						started, parentID = append(started, span), span.Context().SpanID()
					}
					roles := map[uint64]string{}
					for _, side := range sides {
						messageCtx := ctx
						if calls.enabled {
							var call *tracer.Span
							call, messageCtx = cfg.StartCallSpan(ctx, side.component, bidiSpec, &grpcProtocol, nil)
							started, roles[call.Context().SpanID()] = append(started, call), side.name+" call"
						}
						message := startMessageSpan(messageCtx, cfg, side.component)
						started, roles[message.Context().SpanID()] = append(started, message), side.name+" message"
					}
					for _, span := range slices.Backward(started) {
						span.Finish()
					}
					tr.Flush()
					require.Equal(t, len(started), agent.CountSpans())

					sameService := false
					if !parent.none {
						wantParent := cmp.Or(parent.service, test.tracerService)
						assert.Equal(t, wantParent, agent.RequireSpan(t, agenttest.With().Operation("parent")).Service, "parent")
						sameService = wantParent == test.service
					}
					for _, span := range agent.(agenttest.SpanLister).Spans() {
						role, ok := roles[span.SpanID]
						if !ok {
							continue
						}
						assert.Equal(t, test.service, span.Service, role)
						assert.Equal(t, test.source, span.Meta[ext.KeyServiceSource], role)
						// A message span under a call span has the call span's service.
						wantTopLevel := span.ParentID == parentID && !sameService
						_, topLevel := span.Metrics["_dd.top_level"]
						assert.Equal(t, wantTopLevel, topLevel, "%s: _dd.top_level", role)
						wantMeasured := role != "client message" && !wantTopLevel
						assert.Equal(t, wantMeasured, span.Metrics["_dd.measured"] == 1, "%s: _dd.measured", role)
					}
				})
			}
		}
	}

	// With the real tracer and no DD_SERVICE, the spans have the tracer's default service and no
	// service source.
	t.Run("empty default reports no service source", func(t *testing.T) {
		testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
		tr, agent, err := tracertest.Bootstrap(t, tracer.WithLogger(testutils.DiscardLogger()))
		require.NoError(t, err)
		require.Empty(t, newTestConfig().resolveService().name)
		startAllSpanKinds(context.Background(), newTestConfig())
		tr.Flush()
		require.Equal(t, 4, agent.CountSpans())
		assert.Nil(t, agent.FindSpan(agenttest.With().Condition("has a service source", func(s *agenttest.Span) bool {
			_, ok := s.Meta[ext.KeyServiceSource]
			return ok
		})))
	})

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
			testutils.SetGlobalServiceName(t, "") // restored after the tracer, which sets it, stops
			if source.env != "" {
				t.Setenv("DD_SERVICE", source.env)
			}
			cfg := newTestConfig()
			assert.Empty(t, cfg.resolveService().name)
			assert.Nil(t, cfg.service.Load(), "cached before the tracer started")

			tr, agent, err := tracertest.Bootstrap(t, append(source.opts, tracer.WithLogger(testutils.DiscardLogger()))...)
			require.NoError(t, err)
			assert.Equal(t, "started-svc", cfg.resolveService().name)
			require.NotNil(t, cfg.service.Load(), "not cached after the tracer started")
			assert.Same(t, cfg.service.Load(), cfg.resolveService())

			startAllSpanKinds(context.Background(), cfg)
			tr.Flush()
			require.Equal(t, 4, agent.CountSpans())
			assert.Nil(t, agent.FindSpan(agenttest.With().Condition("service is not started-svc", func(s *agenttest.Span) bool {
				return s.Service != "started-svc"
			})))
			// The tracer's service is not an override.
			assert.Nil(t, agent.FindSpan(agenttest.With().Condition("has a service source", func(s *agenttest.Span) bool {
				_, ok := s.Meta[ext.KeyServiceSource]
				return ok
			})))
		})
	}
}
