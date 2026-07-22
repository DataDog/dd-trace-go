// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"maps"
	"slices"
	"testing"
	"time"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	traceinternal "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer/internal"
)

func TestNewTraceFilters(t *testing.T) {
	// Regex keys are literal, so only an invalid regex *value* and an invalid
	// ignore_resources regex are dropped, leaving nothing valid -> nil.
	assert.Nil(t, newTraceFilters(nil, nil, nil, []string{"k:["}, []string{"["}))

	filters := newTraceFilters(
		[]string{" required-key ", " key : value:with:colons "},
		[]string{"reject-key", " reject-kv : reject-value "},
		[]string{" require-regex : value.* ", "present-key"},
		[]string{"reject-regex", "bad:[", " keyonly "},
		[]string{"resource.*", "["},
	)
	require.NotNil(t, filters)
	assert.Equal(t, []string{"required-key"}, filters.requireKeys)
	assert.Equal(t, []tagKV{{key: "key", val: "value:with:colons"}}, filters.requireKV)
	assert.Equal(t, []string{"reject-key"}, filters.rejectKeys)
	assert.Equal(t, []tagKV{{key: "reject-kv", val: "reject-value"}}, filters.rejectKV)

	// Regex filters keep a literal key and a compiled value (nil for key-only);
	// a filter with an invalid value regex is skipped.
	require.Len(t, filters.requireRegex, 2)
	assert.Equal(t, "require-regex", filters.requireRegex[0].key)
	assert.Equal(t, "value.*", filters.requireRegex[0].val.String())
	assert.Equal(t, "present-key", filters.requireRegex[1].key)
	assert.Nil(t, filters.requireRegex[1].val)
	require.Len(t, filters.rejectRegex, 2) // "bad:[" dropped for an invalid value regex
	assert.Equal(t, "reject-regex", filters.rejectRegex[0].key)
	assert.Nil(t, filters.rejectRegex[0].val)
	assert.Equal(t, "keyonly", filters.rejectRegex[1].key)

	require.Len(t, filters.ignoreResources, 1)
	assert.Equal(t, "resource.*", filters.ignoreResources[0].String())
}

func TestTraceFiltersReject(t *testing.T) {
	tests := []struct {
		name      string
		filters   *traceFilters
		operation string
		resource  string
		tags      map[string]string
		reject    bool
	}{
		{
			name:     "ignore resource unanchored",
			filters:  newTraceFilters(nil, nil, nil, nil, []string{"users"}),
			resource: "GET /users/123",
			reject:   true,
		},
		{
			name:      "empty resource defaults to normalized operation",
			filters:   newTraceFilters(nil, nil, nil, nil, []string{"HTTP_Request"}),
			operation: "HTTP Request",
			reject:    true,
		},
		{
			name:     "ignore resources only, non-matching resource is kept",
			filters:  newTraceFilters(nil, nil, nil, nil, []string{"users"}),
			resource: "GET /orders/123",
			tags:     map[string]string{"env": "prod", "peer.service": "db"},
			reject:   false,
		},
		{
			name:    "exact reject key present with empty value",
			filters: newTraceFilters(nil, []string{"blocked"}, nil, nil, nil),
			tags:    map[string]string{"blocked": ""},
			reject:  true,
		},
		{
			name:    "exact values use ordinal equality",
			filters: newTraceFilters(nil, []string{"mode:Prod"}, nil, nil, nil),
			tags:    map[string]string{"mode": "prod"},
			reject:  false,
		},
		{
			name:    "regex reject value is unanchored",
			filters: newTraceFilters(nil, nil, nil, []string{"blocked:alu"}, nil),
			tags:    map[string]string{"blocked": "value"},
			reject:  true,
		},
		{
			name:    "regex reject value is case sensitive",
			filters: newTraceFilters(nil, nil, nil, []string{"blocked:ALU"}, nil),
			tags:    map[string]string{"blocked": "value"},
			reject:  false,
		},
		{
			name:    "missing exact requirement",
			filters: newTraceFilters([]string{"required"}, nil, nil, nil, nil),
			tags:    map[string]string{"other": "value"},
			reject:  true,
		},
		{
			name:    "all exact and regex requirements match",
			filters: newTraceFilters([]string{"required:value"}, nil, []string{"prefix:alu"}, nil, nil),
			tags:    map[string]string{"required": "value", "prefix": "value"},
			reject:  false,
		},
		{
			name:    "reject takes precedence over requirements",
			filters: newTraceFilters([]string{"missing"}, []string{"blocked"}, nil, nil, nil),
			tags:    map[string]string{"blocked": "yes"},
			reject:  true,
		},
		{
			name:    "environment is normalized",
			filters: newTraceFilters(nil, []string{ext.Environment + ":prod_env"}, nil, nil, nil),
			tags:    map[string]string{ext.Environment: "Prod Env"},
			reject:  true,
		},
		{
			name:    "peer.service is normalized before matching",
			filters: newTraceFilters(nil, []string{ext.PeerService + ":my_peer"}, nil, nil, nil),
			tags:    map[string]string{ext.PeerService: "My Peer"},
			reject:  true,
		},
		{
			name:    "require peer.service matches after normalization",
			filters: newTraceFilters([]string{ext.PeerService + ":my_peer"}, nil, nil, nil, nil),
			tags:    map[string]string{ext.PeerService: "My Peer"},
			reject:  false,
		},
		{
			name:    "base service is normalized before matching",
			filters: newTraceFilters(nil, []string{keyBaseService + ":my_svc"}, nil, nil, nil),
			tags:    map[string]string{keyBaseService: "My Svc"},
			reject:  true,
		},
		{
			name:    "require base service matches after normalization",
			filters: newTraceFilters([]string{keyBaseService + ":my_svc"}, nil, nil, nil, nil),
			tags:    map[string]string{keyBaseService: "My Svc"},
			reject:  false,
		},
		{
			name:    "invalid status code is removed",
			filters: newTraceFilters([]string{ext.HTTPCode}, nil, nil, nil, nil),
			tags:    map[string]string{ext.HTTPCode: "999"},
			reject:  true,
		},
		{
			name:    "valid status code remains",
			filters: newTraceFilters([]string{ext.HTTPCode + ":599"}, nil, nil, nil, nil),
			tags:    map[string]string{ext.HTTPCode: "599"},
			reject:  false,
		},
		{
			name:    "regex require key is literal, not matched by an overlapping key",
			filters: newTraceFilters(nil, nil, []string{"version:v1"}, nil, nil),
			tags:    map[string]string{"app.version": "v1"},
			reject:  true,
		},
		{
			name:    "regex reject key is literal, not matched by an overlapping key",
			filters: newTraceFilters(nil, nil, nil, []string{"http.status_code:^5"}, nil),
			tags:    map[string]string{"httpXstatus_code": "500"},
			reject:  false,
		},
		{
			name:    "regex reject matches the literal key value",
			filters: newTraceFilters(nil, nil, nil, []string{"http.status_code:^5"}, nil),
			tags:    map[string]string{ext.HTTPCode: "500"},
			reject:  true,
		},
		{
			name:    "regex require key-only present",
			filters: newTraceFilters(nil, nil, []string{"needed"}, nil, nil),
			tags:    map[string]string{"needed": "anything"},
			reject:  false,
		},
		{
			name:    "regex require key-only missing",
			filters: newTraceFilters(nil, nil, []string{"needed"}, nil, nil),
			tags:    map[string]string{"other": "x"},
			reject:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			span := &Span{
				name:     test.operation,
				resource: test.resource,
				meta:     traceinternal.NewSpanMetaFromMap(test.tags),
			}
			before := span.meta.Map(true)
			var beforeCopy map[string]string
			if before != nil {
				beforeCopy = make(map[string]string, len(before))
				maps.Copy(beforeCopy, before)
			}

			assert.Equal(t, test.reject, test.filters.reject(span))
			assert.Equal(t, beforeCopy, span.meta.Map(true), "filter matching must not mutate span metadata")
			assert.Equal(t, test.resource, span.resource, "filter matching must not mutate the resource")
		})
	}
}

func TestTraceFilterPipeline(t *testing.T) {
	for _, test := range []struct {
		name           string
		filters        *traceFilters
		wantTraces     int
		wantStatsGroup bool
	}{
		{name: "rejected", filters: newTraceFilters(nil, []string{"blocked:true"}, nil, nil, nil)},
		{name: "kept", filters: newTraceFilters(nil, []string{"blocked:false"}, nil, nil, nil), wantTraces: 1, wantStatsGroup: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracer, transport, _, stop, err := startTestTracer(t, WithStatsComputation(true))
			require.NoError(t, err)
			agentFeatures := tracer.config.agent.load()
			agentFeatures.traceFilters = test.filters
			tracer.config.agent.store(agentFeatures)

			span := tracer.StartSpan("trace.filter", ResourceName("resource"), Tag("blocked", "true"), Tag(keyMeasured, 1))
			span.Finish()
			stop()

			assert.Equal(t, test.wantTraces, transport.Len())
			assert.Equal(t, test.wantStatsGroup, hasStatsGroups(transport.Stats()))
		})
	}
}

func TestTraceFilterStatsRefreshSnapshot(t *testing.T) {
	t.Run("filters enabled before root finishes", func(t *testing.T) {
		tracer, transport, _, stop, err := startTestTracer(t, WithStatsComputation(true))
		require.NoError(t, err)

		root := tracer.StartSpan("root", Tag("blocked", "true"), Tag(keyMeasured, 1))
		child := tracer.StartSpan("child", ChildOf(root.Context()), Tag(keyMeasured, 1))
		child.Finish()

		agentFeatures := tracer.config.agent.load()
		agentFeatures.traceFilters = newTraceFilters(nil, []string{"blocked:true"}, nil, nil, nil)
		tracer.config.agent.store(agentFeatures)
		root.Finish()
		stop()

		assert.Zero(t, transport.Len())
		assert.False(t, hasStatsGroups(transport.Stats()), "stats computed before the refresh must be discarded with the rejected root chunk")
	})

	t.Run("CSS disabled before root finishes", func(t *testing.T) {
		tracer, transport, _, stop, err := startTestTracer(t, WithStatsComputation(true))
		require.NoError(t, err)

		root := tracer.StartSpan("root", Tag("blocked", "true"), Tag(keyMeasured, 1))
		child := tracer.StartSpan("child", ChildOf(root.Context()), Tag(keyMeasured, 1))
		child.Finish()

		agentFeatures := tracer.config.agent.load()
		agentFeatures.Stats = false
		agentFeatures.DropP0s = false
		agentFeatures.traceFilters = newTraceFilters(nil, []string{"blocked:true"}, nil, nil, nil)
		tracer.config.agent.store(agentFeatures)
		root.Finish()
		stop()

		assert.Equal(t, 1, transport.Len())
		assert.True(t, hasStatsGroups(transport.Stats()), "the child eligibility snapshot must not be re-gated at chunk assembly")
	})
}

func TestTraceFilterOversizedTraceStats(t *testing.T) {
	oldStartSize, oldMaxSize := traceStartSize, traceMaxSize
	traceStartSize, traceMaxSize = 1, 1
	t.Cleanup(func() {
		traceStartSize, traceMaxSize = oldStartSize, oldMaxSize
	})

	tracer, transport, _, stop, err := startTestTracer(t, WithStatsComputation(true))
	require.NoError(t, err)
	root := tracer.StartSpan("root", Tag(keyMeasured, 1))
	child := tracer.StartSpan("child", ChildOf(root.Context()), Tag(keyMeasured, 1), Tag(ext.SpanKind, ext.SpanKindClient))
	root.Finish()
	child.Finish()
	stop()

	assert.True(t, hasStatsGroups(transport.Stats()), "oversized traces must continue to contribute client-side stats")
}

func TestTraceFilterPartialFlushTagVisibility(t *testing.T) {
	tracer, transport, _, stop, err := startTestTracer(t, WithStatsComputation(true), WithPartialFlushing(1))
	require.NoError(t, err)
	agentFeatures := tracer.config.agent.load()
	agentFeatures.peerTags = []string{"custom.peer"}
	tracer.config.agent.store(agentFeatures)

	root := tracer.StartSpan("root")
	root.context.trace.setTag("custom.peer", "peer-value")
	child := tracer.StartSpan("child", ChildOf(root.Context()), Tag(keyMeasured, 1), Tag(ext.SpanKind, ext.SpanKindClient))
	child.Finish()
	root.Finish()
	stop()

	assert.True(t, statsContainPeerTag(transport.Stats(), "custom.peer:peer-value"),
		"the triggering first-in-chunk span must recompute stats after trace tags are applied")
}

// TestTraceFilterPartialFlushTagVisibilityNonTriggeringFirstSpan verifies tag
// visibility when the chunk's first-finished span (fSpan) is not the span that
// triggers the partial flush. finishedOneLocked applies trace-level tags to
// fSpan, so fSpan's statSpan must be recomputed even though a later span (childB)
// triggers the flush.
func TestTraceFilterPartialFlushTagVisibilityNonTriggeringFirstSpan(t *testing.T) {
	tracer, transport, _, stop, err := startTestTracer(t, WithStatsComputation(true), WithPartialFlushing(2))
	require.NoError(t, err)
	agentFeatures := tracer.config.agent.load()
	agentFeatures.peerTags = []string{"custom.peer"}
	tracer.config.agent.store(agentFeatures)

	root := tracer.StartSpan("root")
	root.context.trace.setTag("custom.peer", "peer-value")
	childA := tracer.StartSpan("childA", ChildOf(root.Context()), Tag(keyMeasured, 1), Tag(ext.SpanKind, ext.SpanKindClient))
	childB := tracer.StartSpan("childB", ChildOf(root.Context()), Tag(keyMeasured, 1), Tag(ext.SpanKind, ext.SpanKindClient))
	childA.Finish() // finished=1 < 2: no flush; childA.statSpan computed without the trace tag
	childB.Finish() // finished=2 >= 2: partial flush with fSpan=childA, s=childB (s != fSpan)
	root.Finish()
	stop()

	assert.True(t, statsContainPeerTag(transport.Stats(), "custom.peer:peer-value"),
		"fSpan (childA) must be recomputed after trace tags are applied, even when it is not the triggering span")
}

func TestTraceFilterPooledSpanClearsStat(t *testing.T) {
	span := &Span{statSpan: &tracerStatSpan{}}
	span.clear()
	assert.Nil(t, span.statSpan)
}

// TestTraceFilterPartialFlushSkipsWithoutRoot verifies that a partial-flush chunk
// that does not contain the local root is passed through unfiltered, even when a
// span in it would match a reject rule. Filtering only applies to the chunk that
// contains the root (matching dd-trace-dotnet).
func TestTraceFilterPartialFlushSkipsWithoutRoot(t *testing.T) {
	tracer, transport, _, stop, err := startTestTracer(t, WithStatsComputation(true), WithPartialFlushing(1))
	require.NoError(t, err)
	agentFeatures := tracer.config.agent.load()
	agentFeatures.traceFilters = newTraceFilters(nil, []string{"blocked:true"}, nil, nil, nil)
	tracer.config.agent.store(agentFeatures)

	root := tracer.StartSpan("root", Tag(keyMeasured, 1))
	child := tracer.StartSpan("child", ChildOf(root.Context()), Tag("blocked", "true"), Tag(keyMeasured, 1), Tag(ext.SpanKind, ext.SpanKindClient))
	child.Finish() // partial flush: chunk=[child], no root -> not filtered (kept)
	root.Finish()  // full flush: chunk=[root], root present, does not match -> kept
	stop()

	stats := transport.Stats()
	assert.True(t, statsContainName(stats, "child"), "a rootless partial-flush chunk is not filtered")
	assert.True(t, statsContainName(stats, "root"))
}

// TestTraceFilterRootChunkStillFiltered verifies filtering still applies to the
// chunk that contains the local root when partial flushing is enabled.
func TestTraceFilterRootChunkStillFiltered(t *testing.T) {
	tracer, transport, _, stop, err := startTestTracer(t, WithStatsComputation(true), WithPartialFlushing(1))
	require.NoError(t, err)
	agentFeatures := tracer.config.agent.load()
	agentFeatures.traceFilters = newTraceFilters(nil, []string{"blocked:true"}, nil, nil, nil)
	tracer.config.agent.store(agentFeatures)

	root := tracer.StartSpan("root", Tag("blocked", "true"), Tag(keyMeasured, 1))
	child := tracer.StartSpan("child", ChildOf(root.Context()), Tag(keyMeasured, 1), Tag(ext.SpanKind, ext.SpanKindClient))
	child.Finish() // partial flush: chunk=[child], no root -> kept
	root.Finish()  // full flush: chunk=[root], root matches blocked:true -> rejected
	stop()

	stats := transport.Stats()
	assert.False(t, statsContainName(stats, "root"), "the root chunk matching the reject rule is dropped")
	assert.True(t, statsContainName(stats, "child"), "the rootless partial-flush chunk is passed through")
}

// TestTraceFilterFullFlushUsesRootDecision verifies that once the root has
// finished (so its decision is known), a full flush applies that decision even
// to a leftover chunk that no longer contains the root.
func TestTraceFilterFullFlushUsesRootDecision(t *testing.T) {
	tracer, transport, _, stop, err := startTestTracer(t, WithStatsComputation(true), WithPartialFlushing(2))
	require.NoError(t, err)
	agentFeatures := tracer.config.agent.load()
	agentFeatures.traceFilters = newTraceFilters(nil, []string{"blocked:true"}, nil, nil, nil)
	tracer.config.agent.store(agentFeatures)

	root := tracer.StartSpan("root", Tag("blocked", "true"), Tag(keyMeasured, 1))
	child1 := tracer.StartSpan("child1", ChildOf(root.Context()), Tag(keyMeasured, 1), Tag(ext.SpanKind, ext.SpanKindClient))
	child2 := tracer.StartSpan("child2", ChildOf(root.Context()), Tag(keyMeasured, 1), Tag(ext.SpanKind, ext.SpanKindClient))
	root.Finish()
	child1.Finish() // partial flush: chunk=[root, child1], root present + matches -> rejected
	child2.Finish() // full flush: leftover chunk=[child2], root finished -> its decision applies -> rejected
	stop()

	stats := transport.Stats()
	assert.False(t, statsContainName(stats, "root"))
	assert.False(t, statsContainName(stats, "child1"))
	assert.False(t, statsContainName(stats, "child2"), "the full-flush leftover uses the finished root's decision")
}

func statsContainName(payloads []*pb.ClientStatsPayload, name string) bool {
	for _, payload := range payloads {
		for _, bucket := range payload.Stats {
			for _, group := range bucket.Stats {
				if group.Name == name {
					return true
				}
			}
		}
	}
	return false
}

func hasStatsGroups(payloads []*pb.ClientStatsPayload) bool {
	for _, payload := range payloads {
		for _, bucket := range payload.Stats {
			if len(bucket.Stats) > 0 {
				return true
			}
		}
	}
	return false
}

func statsContainPeerTag(payloads []*pb.ClientStatsPayload, tag string) bool {
	for _, payload := range payloads {
		for _, bucket := range payload.Stats {
			for _, group := range bucket.Stats {
				if slices.Contains(group.PeerTags, tag) {
					return true
				}
			}
		}
	}
	return false
}

func TestTraceFilterComputeSpanStatsCSSOffClearsStaleValue(t *testing.T) {
	tracer, err := newUnstartedTracer(withNoopInfoHTTPClient(), WithStatsComputation(true))
	require.NoError(t, err)
	defer tracer.statsd.Close()
	span := &Span{statSpan: &tracerStatSpan{}}
	trace := &trace{root: span}
	agentFeatures := tracer.config.agent.load()
	agentFeatures.Stats = false
	agentFeatures.DropP0s = false
	tracer.config.agent.store(agentFeatures)
	tracer.computeSpanStats(trace, span)
	assert.Nil(t, span.statSpan)
	assert.False(t, trace.filterReject)
}

func TestTraceFilterLargeChunkBatchesStats(t *testing.T) {
	cfg, err := newTestConfig(withNoopInfoHTTPClient(), WithStatsComputation(true))
	require.NoError(t, err)
	agentFeatures := cfg.agent.load()
	agentFeatures.Stats = true
	agentFeatures.DropP0s = true
	cfg.agent.store(agentFeatures)
	concentrator := newConcentrator(cfg, defaultStatsBucketSize, nil)
	ticker := time.NewTicker(time.Hour)
	t.Cleanup(ticker.Stop)
	tracer := &tracer{
		config:           cfg,
		stats:            concentrator,
		statsd:           &statsd.NoOpClientDirect{},
		out:              make(chan *chunk, 1),
		stop:             make(chan struct{}),
		logDroppedTraces: ticker,
	}

	const spanCount = 10001
	trace := &trace{}
	chunk := &chunk{spans: make([]*Span, spanCount)}
	for i := range chunk.spans {
		span := &Span{context: &SpanContext{trace: trace}, statSpan: &tracerStatSpan{}}
		chunk.spans[i] = span
	}
	trace.root = chunk.spans[0]
	tracer.submitChunk(chunk)

	require.Len(t, concentrator.In, 1, "one chunk must consume one channel slot")
	assert.Len(t, <-concentrator.In, spanCount)
}
