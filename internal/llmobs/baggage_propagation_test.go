// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs_test

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/llmobstest"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// One 128-bit LLMObs trace ID in the two forms on the wire: baggage carries hex,
// and dd-trace-py writes decimal into the _dd.p.llmobs_trace_id tag.
const (
	upstreamTraceIDHex     = "6928d1a700000000abcdef0123456789"
	upstreamTraceIDDecimal = "139780883686467862853683758018840586121"
)

// baggageItems decodes a baggage header the way an inbound tracer does. W3C
// properties after ";" stay part of the value.
func baggageItems(t *testing.T, header string) map[string]string {
	t.Helper()
	items := map[string]string{}
	if header == "" {
		return items
	}
	for member := range strings.SplitSeq(header, ",") {
		k, v, ok := strings.Cut(member, "=")
		require.True(t, ok, "malformed baggage member %q", member)
		key, err := url.QueryUnescape(strings.TrimSpace(k))
		require.NoError(t, err)
		val, err := url.QueryUnescape(strings.TrimSpace(v))
		require.NoError(t, err)
		items[key] = val
	}
	return items
}

// outboundCapture is a server that records the propagation headers of each request.
type outboundCapture struct {
	mu      sync.Mutex
	baggage []string
	tags    []string
	srv     *httptest.Server
	client  *http.Client
}

func newOutboundCapture(t *testing.T) *outboundCapture {
	c := &outboundCapture{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.baggage = append(c.baggage, r.Header.Get("Baggage"))
		c.tags = append(c.tags, r.Header.Get("X-Datadog-Tags"))
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.srv.Close)
	c.client = traceClient(c.srv.Client())
	return c
}

func (c *outboundCapture) get(t *testing.T, ctx context.Context, header http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.srv.URL, nil)
	require.NoError(t, err)
	maps.Copy(req.Header, header)
	resp, err := c.client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
}

func (c *outboundCapture) last() (baggage, tags string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.baggage[len(c.baggage)-1], c.tags[len(c.tags)-1]
}

// serveInbound sends one request with the given headers to a traced server. The
// server runs handle, then the request ends and the tracer flushes.
func serveInbound(t *testing.T, inbound http.Header, handle func(ctx context.Context)) {
	t.Helper()
	srv := httptest.NewServer(traceHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handle(r.Context())
		w.WriteHeader(http.StatusOK)
	})))
	t.Cleanup(srv.Close)
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	req.Header = inbound
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	tracer.Flush()
}

// serverToolThenCall is the handler of most tests: the server starts an LLMObs
// tool span and makes one outbound request from it.
func serverToolThenCall(t *testing.T, ll *llmobs.LLMObs, out *outboundCapture) func(context.Context) {
	return func(ctx context.Context) {
		tool, ctx := ll.StartSpan(ctx, llmobs.SpanKindTool, "server_tool", llmobs.StartSpanConfig{})
		out.get(t, ctx, nil)
		tool.Finish(llmobs.FinishSpanConfig{})
	}
}

func apmHeaders(xDatadogTags string) http.Header {
	h := http.Header{}
	h.Set("X-Datadog-Trace-Id", "123456789")
	h.Set("X-Datadog-Parent-Id", "987654321")
	h.Set("X-Datadog-Sampling-Priority", "1")
	if xDatadogTags != "" {
		h.Set("X-Datadog-Tags", xDatadogTags)
	}
	return h
}

func TestLLMObsBaggageWrite(t *testing.T) {
	t.Run("a-local-trace-is-sent-in-baggage-with-a-hex-trace-id", func(t *testing.T) {
		_, _, ll := testTracer(t)
		out := newOutboundCapture(t)

		span, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindWorkflow, "root", llmobs.StartSpanConfig{SessionID: "sess-1"})
		out.get(t, ctx, nil)
		span.Finish(llmobs.FinishSpanConfig{})

		baggage, tags := out.last()
		items := baggageItems(t, baggage)
		assert.Equal(t, span.SpanID(), items["llmobs.parent_id"])
		assert.Equal(t, span.TraceID(), items["llmobs.trace_id"])
		assert.Len(t, items["llmobs.trace_id"], 32)
		assert.Equal(t, "gotest", items["llmobs.ml_app"])
		assert.Equal(t, "sess-1", items["llmobs.session_id"])
		// The tag carrier is unchanged: a trace that starts in Go keeps its hex ID there.
		assert.Contains(t, tags, "_dd.p.llmobs_parent_id="+span.SpanID())
		assert.Contains(t, tags, "_dd.p.llmobs_trace_id="+span.TraceID())
	})
	t.Run("llmobs-items-are-not-stored-in-span-baggage", func(t *testing.T) {
		// Items stored in a span's baggage are copied into every child span. The
		// items are made at injection instead.
		_, _, ll := testTracer(t)
		span, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindWorkflow, "root", llmobs.StartSpanConfig{})
		defer span.Finish(llmobs.FinishSpanConfig{})

		child, _ := tracer.StartSpanFromContext(ctx, "child")
		defer child.Finish()
		child.Context().ForeachBaggageItem(func(k, _ string) bool {
			assert.False(t, strings.HasPrefix(k, "llmobs."), "unexpected baggage item %q", k)
			return true
		})
	})
	t.Run("llmobs-items-go-first-and-survive-the-item-cap", func(t *testing.T) {
		_, _, ll := testTracer(t)
		out := newOutboundCapture(t)

		span, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindWorkflow, "root", llmobs.StartSpanConfig{})
		defer span.Finish(llmobs.FinishSpanConfig{})
		apm, ctx := tracer.StartSpanFromContext(ctx, "work")
		defer apm.Finish()
		for i := range 64 {
			apm.SetBaggageItem("user"+strconv.Itoa(i), "v")
		}
		for range 20 {
			out.get(t, ctx, nil)
			baggage, _ := out.last()
			require.True(t, strings.HasPrefix(baggage, "llmobs.parent_id="), "llmobs items must lead the header")
			items := baggageItems(t, baggage)
			assert.Len(t, items, 64)
			assert.Equal(t, span.TraceID(), items["llmobs.trace_id"])
		}
	})
	t.Run("concurrent-requests-each-carry-their-own-parent", func(t *testing.T) {
		_, _, ll := testTracer(t)
		out := newOutboundCapture(t)

		root, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindAgent, "agent", llmobs.StartSpanConfig{})
		defer root.Finish(llmobs.FinishSpanConfig{})

		const n = 8
		want := make(map[string]bool, n)
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() {
				tool, ctx := ll.StartSpan(ctx, llmobs.SpanKindTool, "tool"+strconv.Itoa(i), llmobs.StartSpanConfig{})
				defer tool.Finish(llmobs.FinishSpanConfig{})
				mu.Lock()
				want[tool.SpanID()] = true
				mu.Unlock()
				out.get(t, ctx, nil)
			})
		}
		wg.Wait()

		got := make(map[string]bool, n)
		for _, b := range out.baggage {
			got[baggageItems(t, b)["llmobs.parent_id"]] = true
		}
		assert.Equal(t, want, got)
	})
	t.Run("a-caller-baggage-header-is-merged", func(t *testing.T) {
		_, _, ll := testTracer(t)
		out := newOutboundCapture(t)

		span, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindWorkflow, "root", llmobs.StartSpanConfig{})
		defer span.Finish(llmobs.FinishSpanConfig{})
		caller := http.Header{}
		caller.Set("Baggage", "tenant=acme;scope=internal,llmobs.parent_id=1")
		out.get(t, ctx, caller)

		baggage, _ := out.last()
		items := baggageItems(t, baggage)
		assert.Equal(t, "acme;scope=internal", items["tenant"], "the caller's member and its properties are kept")
		assert.Equal(t, span.SpanID(), items["llmobs.parent_id"], "the caller's llmobs item is replaced")
		assert.Equal(t, 1, strings.Count(baggage, "llmobs.parent_id="))
	})
	t.Run("a-caller-baggage-header-is-merged-outside-an-llmobs-span-when-llmobs-is-enabled", func(t *testing.T) {
		_, _, _ = testTracer(t)
		out := newOutboundCapture(t)

		apm, ctx := tracer.StartSpanFromContext(context.Background(), "work")
		defer apm.Finish()
		apm.SetBaggageItem("k", "v")
		caller := http.Header{}
		caller.Set("Baggage", "tenant=acme")
		out.get(t, ctx, caller)

		baggage, _ := out.last()
		assert.Equal(t, "k=v,tenant=acme", baggage)
	})
	t.Run("a-reused-carrier-drops-the-earlier-requests-llmobs-items", func(t *testing.T) {
		_, _, ll := testTracer(t)

		for _, tt := range []struct {
			name, callerHeader, want string
		}{
			{"only-stale-items", "", ""},
			{"stale-items-and-caller-members", "tenant=a", "tenant=a"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				carrier := tracer.TextMapCarrier{}
				if tt.callerHeader != "" {
					carrier["baggage"] = tt.callerHeader
				}
				span, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindWorkflow, "root", llmobs.StartSpanConfig{})
				apm, _ := tracer.StartSpanFromContext(ctx, "first-request")
				require.NoError(t, tracer.Inject(apm.Context(), carrier))
				apm.Finish()
				span.Finish(llmobs.FinishSpanConfig{})
				require.Contains(t, carrier["baggage"], "llmobs.parent_id=")

				other, _ := tracer.StartSpanFromContext(context.Background(), "second-request")
				require.NoError(t, tracer.Inject(other.Context(), carrier))
				other.Finish()

				got, ok := carrier["baggage"]
				if tt.want == "" {
					assert.False(t, ok, "a header left with only stale llmobs items is removed, got %q", got)
					return
				}
				assert.Equal(t, tt.want, got)
			})
		}
	})
}

func TestLLMObsBaggageRead(t *testing.T) {
	t.Run("tags-alone-continue-the-trace-and-the-trace-id-becomes-hex", func(t *testing.T) {
		// A caller on an older tracer sends only the tags, with a decimal trace ID.
		_, coll, ll := testTracer(t)
		out := newOutboundCapture(t)
		inbound := apmHeaders("_dd.p.llmobs_parent_id=555,_dd.p.llmobs_trace_id=" + upstreamTraceIDDecimal + ",_dd.p.llmobs_ml_app=upstream")

		serveInbound(t, inbound, serverToolThenCall(t, ll, out))

		tool := coll.RequireSpan(t, "server_tool")
		assert.Equal(t, "555", tool.ParentID)
		assert.Equal(t, upstreamTraceIDHex, tool.TraceID)
		baggage, tags := out.last()
		assert.Equal(t, upstreamTraceIDHex, baggageItems(t, baggage)["llmobs.trace_id"], "baggage carries hex")
		assert.Contains(t, tags, "_dd.p.llmobs_trace_id="+upstreamTraceIDDecimal, "the tag keeps the form it arrived in")
	})
	t.Run("restart-does-not-continue-the-llmobs-trace", func(t *testing.T) {
		t.Setenv("DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT", "restart")
		_, coll, ll := testTracer(t)
		out := newOutboundCapture(t)
		inbound := apmHeaders("")
		inbound.Set("Baggage", "llmobs.parent_id=555,llmobs.trace_id="+upstreamTraceIDHex+",llmobs.ml_app=upstream,llmobs.session_id=sess-up,tenant=acme")

		serveInbound(t, inbound, serverToolThenCall(t, ll, out))

		tool := coll.RequireSpan(t, "server_tool")
		assert.Equal(t, llmobs.DefaultParentID, tool.ParentID)
		assert.NotEqual(t, upstreamTraceIDHex, tool.TraceID)
		assert.Empty(t, tool.SessionID)
		baggage, _ := out.last()
		items := baggageItems(t, baggage)
		assert.Equal(t, "acme", items["tenant"], "other baggage still propagates")
		assert.Equal(t, tool.SpanID, items["llmobs.parent_id"])
	})
	t.Run("a-pass-through-hop-forwards-the-context-in-canonical-form", func(t *testing.T) {
		// The server makes a request without an LLMObs span of its own.
		_, _, _ = testTracer(t)
		out := newOutboundCapture(t)
		inbound := apmHeaders("_dd.p.llmobs_parent_id=555,_dd.p.llmobs_trace_id=" + upstreamTraceIDDecimal + ",_dd.p.llmobs_sid=sess-up,_dd.p.llmobs_sd=1")

		serveInbound(t, inbound, func(ctx context.Context) { out.get(t, ctx, nil) })

		baggage, _ := out.last()
		items := baggageItems(t, baggage)
		assert.Equal(t, "555", items["llmobs.parent_id"])
		assert.Equal(t, upstreamTraceIDHex, items["llmobs.trace_id"])
		assert.Equal(t, "sess-up", items["llmobs.session_id"])
		assert.Equal(t, "1", items["llmobs.sampling_decision"], "dd-trace-py's decision is forwarded")
	})
	t.Run("a-pass-through-hop-after-a-baggage-only-hop-also-writes-the-tags", func(t *testing.T) {
		// As in dd-trace-py, so that a downstream tracer that reads only the tags
		// still continues the trace. The tag trace ID is decimal.
		_, _, _ = testTracer(t)
		out := newOutboundCapture(t)
		inbound := http.Header{}
		inbound.Set("Baggage", "llmobs.parent_id=555,llmobs.trace_id="+upstreamTraceIDHex+",llmobs.ml_app=upstream,llmobs.session_id=sess-up,llmobs.sampling_decision=0")

		serveInbound(t, inbound, func(ctx context.Context) { out.get(t, ctx, nil) })

		_, tags := out.last()
		assert.Contains(t, tags, "_dd.p.llmobs_sd=0")
		assert.Contains(t, tags, "_dd.p.llmobs_parent_id=555")
		assert.Contains(t, tags, "_dd.p.llmobs_trace_id="+upstreamTraceIDDecimal)
		assert.Contains(t, tags, "_dd.p.llmobs_ml_app=upstream")
		assert.Contains(t, tags, "_dd.p.llmobs_sid=sess-up")
	})
	t.Run("a-plain-span-does-not-reset-the-tags-while-an-llmobs-span-is-open", func(t *testing.T) {
		// A goroutine starts an APM span from the request context while LLMObs span L
		// is open. A request injected under L must still name L in both carriers.
		_, _, ll := testTracer(t)
		inbound := http.Header{}
		inbound.Set("Baggage", "llmobs.parent_id=555,llmobs.trace_id="+upstreamTraceIDHex)
		carrier := tracer.TextMapCarrier{}
		var l *llmobs.Span

		serveInbound(t, inbound, func(ctx context.Context) {
			var lctx context.Context
			l, lctx = ll.StartSpan(ctx, llmobs.SpanKindWorkflow, "l", llmobs.StartSpanConfig{})
			underL, _ := tracer.StartSpanFromContext(lctx, "under-l")
			plain, _ := tracer.StartSpanFromContext(ctx, "plain")
			plain.Finish()
			require.NoError(t, tracer.Inject(underL.Context(), carrier))
			underL.Finish()
			l.Finish(llmobs.FinishSpanConfig{})
		})

		assert.Equal(t, l.SpanID(), baggageItems(t, carrier["baggage"])["llmobs.parent_id"])
		assert.Contains(t, carrier["x-datadog-tags"], "_dd.p.llmobs_parent_id="+l.SpanID())
	})

	t.Run("inbound-llmobs-baggage-is-replaced-not-forwarded", func(t *testing.T) {
		_, coll, ll := testTracer(t)
		out := newOutboundCapture(t)
		inbound := http.Header{}
		inbound.Set("Baggage", "llmobs.parent_id=555,llmobs.trace_id="+upstreamTraceIDHex+",llmobs.parent_agent_span_id=555,llmobs.parent_agent_name=up,llmobs.sampling_decision=0")

		serveInbound(t, inbound, serverToolThenCall(t, ll, out))

		baggage, _ := out.last()
		items := baggageItems(t, baggage)
		assert.Equal(t, coll.RequireSpan(t, "server_tool").SpanID, items["llmobs.parent_id"])
		assert.Equal(t, 1, strings.Count(baggage, "llmobs.parent_id="))
		assert.Equal(t, "0", items["llmobs.sampling_decision"], "written from the continued context, not forwarded")
	})
	t.Run("each-inbound-request-is-counted-once-with-its-carrier", func(t *testing.T) {
		rec := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(rec)()
		_, _, ll := testTracer(t)
		out := newOutboundCapture(t)

		for _, inbound := range []http.Header{
			func() http.Header {
				h := http.Header{}
				h.Set("Baggage", "llmobs.parent_id=111,llmobs.trace_id="+upstreamTraceIDHex)
				return h
			}(),
			apmHeaders("_dd.p.llmobs_parent_id=222,_dd.p.llmobs_trace_id=" + upstreamTraceIDDecimal),
			apmHeaders(""),
			func() http.Header {
				h := apmHeaders("_dd.p.llmobs_parent_id=222,_dd.p.llmobs_trace_id=" + upstreamTraceIDDecimal)
				h.Set("Baggage", "llmobs.parent_id=111,llmobs.trace_id="+upstreamTraceIDHex)
				return h
			}(),
		} {
			serveInbound(t, inbound, serverToolThenCall(t, ll, out))
		}

		counts := map[string]float64{}
		for key, handle := range rec.Metrics {
			if key.Namespace == telemetry.NamespaceMLObs && key.Name == "activate_distributed_headers" {
				counts[key.Tags] = handle.Get()
			}
		}
		assert.Equal(t, map[string]float64{
			"carrier:baggage,error:0":                           1,
			"carrier:stale_baggage,error:0":                     1,
			"carrier:tags,error:0":                              1,
			"carrier:none,error:1,error_type:missing_parent_id": 1,
		}, counts)
	})
}

func TestLLMObsBaggageReadWithStrippedHeaders(t *testing.T) {
	// Two Go services, with an intermediary between them that drops some headers.
	run := func(t *testing.T, drop func(string) bool) (upstream *llmobs.Span, downstream *llmobstest.LLMObsSpan) {
		t.Helper()
		_, coll, ll := testTracer(t)
		inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			tool, _ := ll.StartSpan(req.Context(), llmobs.SpanKindTool, "server_tool", llmobs.StartSpanConfig{})
			tool.Finish(llmobs.FinishSpanConfig{})
			w.WriteHeader(http.StatusOK)
		})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			for k := range req.Header {
				if drop(strings.ToLower(k)) {
					req.Header.Del(k)
				}
			}
			traceHandler(inner).ServeHTTP(w, req)
		}))
		t.Cleanup(srv.Close)
		cl := traceClient(srv.Client())

		span, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindWorkflow, "client_workflow", llmobs.StartSpanConfig{SessionID: "sess-read"})
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		resp, err := cl.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		span.Finish(llmobs.FinishSpanConfig{})
		tracer.Flush()
		return span, coll.RequireSpan(t, "server_tool")
	}
	isAPMHeader := func(k string) bool {
		return strings.HasPrefix(k, "x-datadog-") || k == "traceparent" || k == "tracestate"
	}
	isBaggage := func(k string) bool { return k == "baggage" }

	for _, tt := range []struct {
		name  string
		drop  func(string) bool
		joins bool
	}{
		{"apm-headers-dropped", isAPMHeader, true},
		{"baggage-dropped", isBaggage, true},
		{"both-dropped", func(k string) bool { return isAPMHeader(k) || isBaggage(k) }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream, tool := run(t, tt.drop)
			if !tt.joins {
				assert.NotEqual(t, upstream.TraceID(), tool.TraceID)
				assert.Equal(t, llmobs.DefaultParentID, tool.ParentID)
				return
			}
			assert.Equal(t, upstream.TraceID(), tool.TraceID)
			assert.Equal(t, upstream.SpanID(), tool.ParentID)
			assert.Equal(t, "sess-read", tool.SessionID)
		})
	}
}

// BenchmarkAPMSpanUnderLLMObsSpan measures the cost of starting an APM span
// under an active LLMObs span, which pays for LLMObs propagation.
func BenchmarkAPMSpanUnderLLMObsSpan(b *testing.B) {
	_, _, ll := testTracer(b)
	root, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindWorkflow, "root", llmobs.StartSpanConfig{SessionID: "s"})
	defer root.Finish(llmobs.FinishSpanConfig{})
	b.ReportAllocs()
	for b.Loop() {
		s, _ := tracer.StartSpanFromContext(ctx, "child")
		s.Finish()
	}
}
