// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// This file is in an external test package: it starts a real tracer, and
// ddtrace/tracer imports internal/appsec, which imports the package under test.
package httpsec_test

import (
	"context"
	_ "embed" // For go:embed
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/go-libddwaf/v5"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	emitterhttpsec "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/apisec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	wafemitter "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/httpsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

//go:embed testdata/keep_limiter_rules.json
var keepLimiterRulesJSON []byte

// countingLimiter is a limiter that counts its Allow calls. It denies when deny is true.
type countingLimiter struct {
	deny  atomic.Bool
	calls atomic.Int64
}

func (l *countingLimiter) Allow() bool {
	l.calls.Add(1)
	return !l.deny.Load()
}

// switchSampler is an API Security sampler that the test controls. It selects
// the requests only when on is true.
type switchSampler struct{ on atomic.Bool }

func (s *switchSampler) DecisionFor(apisec.SamplingKey) bool { return s.on.Load() }

// keepLimiterHarness runs HTTP requests through the real tracer, the real WAF
// and the HTTP listener. Only the limiter and the API Security sampler are fakes.
type keepLimiterHarness struct {
	t       *testing.T
	tracer  tracer.Tracer
	agent   agenttest.Agent
	client  *telemetrytest.RecordClient
	rootOp  dyngo.Operation
	limiter *countingLimiter
	sampler *switchSampler
}

type keepLimiterSetup struct {
	// standalone starts the tracer with APM tracing disabled (DD_APM_TRACING_ENABLED=false).
	standalone bool
	// sampleRate is DD_TRACE_SAMPLE_RATE, for the APM mode.
	sampleRate string
	// apiSec enables API Security schema extraction, with a sampler that the test controls.
	apiSec bool
}

// newKeepLimiterHarness uses the global tracer and the global telemetry client, so
// the tests that use it must not run in parallel.
func newKeepLimiterHarness(t *testing.T, setup keepLimiterSetup) *keepLimiterHarness {
	t.Helper()
	if usable, err := libddwaf.Usable(); !usable {
		t.Skipf("libddwaf is not usable in this context: %v", err)
	}

	// The tracer must not start AppSec itself: the test builds the features.
	t.Setenv("DD_APPSEC_ENABLED", "false")
	if setup.standalone {
		t.Setenv("DD_APM_TRACING_ENABLED", "false")
	} else {
		t.Setenv("DD_TRACE_SAMPLE_RATE", setup.sampleRate)
	}
	tr, agent, err := tracertest.Bootstrap(t, tracer.WithLogStartup(false))
	require.NoError(t, err)

	// The telemetry recorder must be in place before the features start: they create their metric handles at start.
	client := testutils.StartTelemetryRecorder(t)

	wafManager, err := config.NewWAFManagerWithStaticRules(config.ObfuscatorConfig{}, keepLimiterRulesJSON)
	require.NoError(t, err)
	t.Cleanup(wafManager.Close)

	sampler := new(switchSampler)
	cfg := config.Config{
		WAFManager:     wafManager,
		WAFTimeout:     time.Hour,
		TraceRateLimit: 1_000,
		APISec:         config.APISecConfig{Enabled: setup.apiSec, Sampler: sampler},
		SupportedAddresses: config.NewAddressSet([]string{
			"server.request.headers.no_cookies",
			"server.response.headers.no_cookies",
		}),
		MetaStructAvailable: false,
		TracingAsTransport:  setup.standalone,
	}

	rootOp := dyngo.NewRootOperation()
	wafFeature, err := waf.NewWAFFeature(&cfg, rootOp)
	require.NoError(t, err)
	t.Cleanup(wafFeature.Stop)

	// dyngo calls the listeners in the order of their registration. This one comes after the
	// listener of the WAF feature, which sets the real limiter. So the fake replaces the real one
	// for the rest of the request.
	lim := new(countingLimiter)
	dyngo.On(rootOp, func(op *wafemitter.ContextOperation, _ wafemitter.ContextArgs) {
		op.SetLimiter(lim)
	})

	httpFeature, err := httpsec.NewHTTPSecFeature(&cfg, rootOp)
	require.NoError(t, err)
	t.Cleanup(httpFeature.Stop)

	return &keepLimiterHarness{
		t:       t,
		tracer:  tr,
		agent:   agent,
		client:  client,
		rootOp:  rootOp,
		limiter: lim,
		sampler: sampler,
	}
}

// keepLimiterRequest describes one request.
type keepLimiterRequest struct {
	// resource is unique for each request of a test, because the agent finds a span by resource.
	resource  string
	userAgent string
	// manualDrop drops the trace after the request phase and before the response phase.
	manualDrop      bool
	responseHeaders map[string][]string
}

// do sends the request, and returns the span that the mock agent received.
func (h *keepLimiterHarness) do(req keepLimiterRequest) *agenttest.Span {
	h.t.Helper()

	span := h.tracer.StartSpan("http.request", tracer.ResourceName(req.resource))
	// Each request is bound to the root operation of the test, as the other tests of this package do.
	ctx := dyngo.RegisterOperation(context.Background(), h.rootOp)
	op, _, _ := emitterhttpsec.StartOperation(ctx, emitterhttpsec.HandlerOperationArgs{
		Framework:    "test/phony",
		Method:       "GET",
		RequestURI:   "/fake/test/uri",
		RequestRoute: "/fake/:id/uri",
		Host:         "localhost",
		RemoteAddr:   "127.0.0.1:4242",
		ClientIP:     netip.MustParseAddr("127.0.0.1"),
		Headers:      map[string][]string{"user-agent": {req.userAgent}},
		Cookies:      map[string][]string{},
		QueryParams:  map[string][]string{},
		PathParams:   map[string]string{"id": "test"},
	}, span)
	if req.manualDrop {
		span.SetTag(ext.ManualDrop, true)
	}
	op.Finish(emitterhttpsec.HandlerOperationRes{StatusCode: 200, Headers: req.responseHeaders})
	span.Finish()
	h.tracer.Flush()

	return h.agent.RequireSpan(h.t, agenttest.With().Resource(req.resource))
}

// wafRequests sums the waf.requests series that have all the given tags.
func (h *keepLimiterHarness) wafRequests(tags ...string) float64 {
	var sum float64
	for key, handle := range h.client.Metrics {
		if key.Namespace != telemetry.NamespaceAppSec || key.Name != "waf.requests" {
			continue
		}
		keyTags := strings.Split(key.Tags, ",")
		matches := true
		for _, tag := range tags {
			matches = matches && slices.Contains(keyTags, tag)
		}
		if matches {
			sum += handle.Get()
		}
	}
	return sum
}

// samplingPriority returns the sampling priority of the span, and false when it has none.
func samplingPriority(span *agenttest.Span) (float64, bool) {
	v, ok := span.Metrics["_sampling_priority_v1"]
	return v, ok
}

// requirePriority checks the sampling priority and the decision maker of the span.
// An empty decisionMaker means that the span has no decision maker.
func requirePriority(t *testing.T, span *agenttest.Span, priority float64, decisionMaker string) {
	t.Helper()
	got, ok := samplingPriority(span)
	require.True(t, ok, "the span has no sampling priority")
	require.Equal(t, priority, got, "sampling priority")
	require.Equal(t, decisionMaker, span.Meta["_dd.p.dm"], "decision maker")
}

// The warm-up request makes the rules keep of the first request, which is not
// the subject of the test. The test checks its result, so a change of the
// tracer behavior fails the test here, and it does not give a false pass.
func (h *keepLimiterHarness) warmUp() {
	h.t.Helper()
	span := h.do(keepLimiterRequest{resource: "warm-up", userAgent: "KeepLimiter/clean"})
	requirePriority(h.t, span, 2, "-5")
	require.Zero(h.t, h.limiter.calls.Load(), "the warm-up must not call the limiter")
}

// baseline checks that, without any keep, a standalone trace is not kept.
func (h *keepLimiterHarness) baseline() {
	h.t.Helper()
	span := h.do(keepLimiterRequest{resource: "baseline", userAgent: "KeepLimiter/clean"})
	got, ok := samplingPriority(span)
	require.True(h.t, ok, "the span has no sampling priority")
	require.LessOrEqual(h.t, got, 0.0, "the baseline request must not be kept")
}

// The IDs of the rules of testdata/keep_limiter_rules.json.
const (
	ruleKeepEvent   = "kl-001" // request: keep, event
	ruleKeepNoEvent = "kl-002" // request: keep, no event, with attributes
	ruleNoKeepEvent = "kl-003" // request: no keep, event
	ruleResponse    = "kl-004" // response: keep, event
)

func TestKeepLimiter(t *testing.T) {
	again := map[string][]string{"x-keep-limiter": {"again"}}

	for _, tc := range []struct {
		name  string
		setup keepLimiterSetup
		// noWarmUp skips the warm-up request, so the tested request is the first request of the WAF feature.
		noWarmUp bool
		// baseline sends a clean request first, and checks that the trace is not kept.
		baseline bool
		// schemaKeep selects the tested request for the API Security schema extraction.
		schemaKeep bool
		deny       bool
		request    keepLimiterRequest

		wantPriority      float64
		wantDecisionMaker string
		// wantRules are the IDs of the rules in _dd.appsec.json. Empty means that the span has no event.
		wantRules []string
		// wantCalls is the number of limiter calls of the tested request.
		wantCalls         int64
		wantRateLimited   bool
		wantRuleTriggered bool
	}{
		{
			name:              "apm-sampled/keep-event/denied",
			setup:             keepLimiterSetup{sampleRate: "1"},
			deny:              true,
			request:           keepLimiterRequest{userAgent: "KeepLimiter/event"},
			wantPriority:      2,
			wantDecisionMaker: "-3", // the sampler keeps the trace, not the limiter
			wantRules:         []string{ruleKeepEvent},
			wantCalls:         1,
			wantRateLimited:   true,
			wantRuleTriggered: true,
		},
		{
			name:              "apm-dropped/keep-event/denied",
			setup:             keepLimiterSetup{sampleRate: "0"},
			deny:              true,
			request:           keepLimiterRequest{userAgent: "KeepLimiter/event"},
			wantPriority:      -1,
			wantRules:         []string{ruleKeepEvent}, // a denial does not remove the events
			wantCalls:         1,
			wantRateLimited:   true,
			wantRuleTriggered: true,
		},
		{
			name:              "apm-dropped/keep-event/allowed",
			setup:             keepLimiterSetup{sampleRate: "0"},
			request:           keepLimiterRequest{userAgent: "KeepLimiter/event"},
			wantPriority:      2,
			wantDecisionMaker: "-5",
			wantRules:         []string{ruleKeepEvent},
			wantCalls:         1,
			wantRuleTriggered: true,
		},
		{
			name:              "apm-sampled/keep-no-event/denied",
			setup:             keepLimiterSetup{sampleRate: "1"},
			deny:              true,
			request:           keepLimiterRequest{userAgent: "KeepLimiter/noevent"},
			wantPriority:      2,
			wantDecisionMaker: "-3", // not -5: the limiter denied the keep
			wantCalls:         1,
			wantRateLimited:   true,
		},
		{
			name:              "apm-dropped/no-keep-event/denied",
			setup:             keepLimiterSetup{sampleRate: "0"},
			deny:              true,
			request:           keepLimiterRequest{userAgent: "KeepLimiter/nokeep"},
			wantPriority:      -1,
			wantRules:         []string{ruleNoKeepEvent},
			wantCalls:         0, // a result without keep does not use a token
			wantRuleTriggered: true,
		},
		{
			name:              "standalone/keep-event/denied",
			setup:             keepLimiterSetup{standalone: true},
			baseline:          true,
			deny:              true,
			request:           keepLimiterRequest{userAgent: "KeepLimiter/event"},
			wantPriority:      -1,
			wantRules:         []string{ruleKeepEvent},
			wantCalls:         1,
			wantRateLimited:   true,
			wantRuleTriggered: true,
		},
		{
			// The first request of a WAF feature gets the rules keep, whatever the limiter returns.
			name:              "apm-dropped/first-request/keep-event/denied",
			setup:             keepLimiterSetup{sampleRate: "0"},
			noWarmUp:          true,
			deny:              true,
			request:           keepLimiterRequest{userAgent: "KeepLimiter/event"},
			wantPriority:      2,
			wantDecisionMaker: "-5",
			wantRules:         []string{ruleKeepEvent},
			wantCalls:         1,
			wantRateLimited:   true,
			wantRuleTriggered: true,
		},
		{
			// The API Security schema keep is not a WAF keep: the limiter does not see it.
			name:              "standalone/schema-keep/keep-event/denied",
			setup:             keepLimiterSetup{standalone: true, apiSec: true},
			baseline:          true,
			schemaKeep:        true,
			deny:              true,
			request:           keepLimiterRequest{userAgent: "KeepLimiter/event"},
			wantPriority:      2,
			wantDecisionMaker: "-5",
			wantRules:         []string{ruleKeepEvent},
			wantCalls:         1,
			wantRateLimited:   true,
			wantRuleTriggered: true,
		},
		{
			// After the first admission, the request calls the limiter no more. The second keep
			// run sets ManualKeep again, and it puts back the keep that the user code dropped.
			name:              "apm-dropped/keep-again-after-manual-drop",
			setup:             keepLimiterSetup{sampleRate: "0"},
			request:           keepLimiterRequest{userAgent: "KeepLimiter/event", manualDrop: true, responseHeaders: again},
			wantPriority:      2,
			wantDecisionMaker: "-5",
			wantRules:         []string{ruleKeepEvent, ruleResponse},
			wantCalls:         1,
			wantRuleTriggered: true,
		},
		{
			// Control for the case above: the manual drop works when there is no second keep.
			name:              "apm-sampled/manual-drop/no-second-keep",
			setup:             keepLimiterSetup{sampleRate: "1"},
			request:           keepLimiterRequest{userAgent: "KeepLimiter/event", manualDrop: true},
			wantPriority:      -1,
			wantRules:         []string{ruleKeepEvent},
			wantCalls:         1,
			wantRuleTriggered: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newKeepLimiterHarness(t, tc.setup)

			if !tc.noWarmUp {
				h.warmUp()
			}
			if tc.baseline {
				h.baseline()
			}

			// Use the counters after the setup requests as the reference: the test looks at the
			// tested request only.
			callsBefore := h.limiter.calls.Load()
			totalBefore := h.wafRequests()
			limitedBefore := h.wafRequests("rate_limited:true")
			wantTagsBefore := h.wafRequests(
				"rate_limited:"+boolString(tc.wantRateLimited),
				"rule_triggered:"+boolString(tc.wantRuleTriggered),
			)

			h.limiter.deny.Store(tc.deny)
			h.sampler.on.Store(tc.schemaKeep)
			tc.request.resource = "tested"
			span := h.do(tc.request)
			h.sampler.on.Store(false)

			requirePriority(t, span, tc.wantPriority, tc.wantDecisionMaker)

			appsecJSON := span.Meta["_dd.appsec.json"]
			if len(tc.wantRules) == 0 {
				require.Empty(t, appsecJSON, "the span must have no event")
			}
			for _, id := range tc.wantRules {
				require.Contains(t, appsecJSON, `"id":"`+id+`"`, "the span must have the event of rule %s", id)
			}
			for _, id := range []string{ruleKeepEvent, ruleKeepNoEvent, ruleNoKeepEvent, ruleResponse} {
				if !slices.Contains(tc.wantRules, id) {
					require.NotContains(t, appsecJSON, `"id":"`+id+`"`, "the span must not have the event of rule %s", id)
				}
			}

			require.Equal(t, tc.wantCalls, h.limiter.calls.Load()-callsBefore, "limiter calls of the tested request")
			require.Equal(t, 1.0, h.wafRequests()-totalBefore, "waf.requests of the tested request")
			wantLimited := 0.0
			if tc.wantRateLimited {
				wantLimited = 1
			}
			require.Equal(t, wantLimited, h.wafRequests("rate_limited:true")-limitedBefore, "waf.requests with rate_limited:true")
			require.Equal(t, 1.0, h.wafRequests(
				"rate_limited:"+boolString(tc.wantRateLimited),
				"rule_triggered:"+boolString(tc.wantRuleTriggered),
			)-wantTagsBefore, "waf.requests with the expected tags")
		})
	}
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
