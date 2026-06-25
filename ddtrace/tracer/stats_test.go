// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/datadog-agent/pkg/obfuscate"
	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	"github.com/DataDog/datadog-go/v5/statsd"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	tinternal "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
)

func TestAlignTs(t *testing.T) {
	now := time.Now().UnixNano()
	got := alignTs(now, defaultStatsBucketSize)
	want := now - now%((10 * time.Second).Nanoseconds())
	assert.Equal(t, got, want)
}

func newTestConfigWithTransportAndEnv(t *testing.T, transport ddTransport, env string) *config {
	assert := assert.New(t)
	cfg, err := newTestConfig(withNoopInfoHTTPClient(), func(c *config) {
		c.ddTransport = transport
		c.internalConfig.SetEnv(env, internalconfig.OriginCode)
	})
	assert.NoError(err)
	return cfg
}

func newTestConfigWithTransport(t *testing.T, transport ddTransport) *config {
	assert := assert.New(t)
	cfg, err := newTestConfig(withNoopInfoHTTPClient(), func(c *config) {
		c.ddTransport = transport
	})
	assert.NoError(err)
	return cfg
}

func additionalMetricTagsCardinalityLimit(c *concentrator) int {
	limits := reflect.ValueOf(c.spanConcentrator).Elem().FieldByName("cardinalityLimits")
	return int(limits.FieldByName("AdditionalTags").Int())
}

func TestConcentrator(t *testing.T) {
	bucketSize := int64(500_000)
	s1 := Span{
		name:     "http.request",
		start:    time.Now().UnixNano() + 3*bucketSize,
		duration: 1,
		metrics:  map[string]float64{keyMeasured: 1},
	}
	s2 := Span{
		name:     "sql.query",
		start:    time.Now().UnixNano() + 4*bucketSize,
		duration: 1,
		metrics:  map[string]float64{keyMeasured: 1},
	}
	t.Run("start-stop", func(t *testing.T) {
		assert := assert.New(t)
		cfg, err := newTestConfig()
		assert.NoError(err)
		c := newConcentrator(cfg, bucketSize, &statsd.NoOpClientDirect{})
		assert.EqualValues(atomic.LoadUint32(&c.stopped), 1)
		c.Start()
		assert.EqualValues(atomic.LoadUint32(&c.stopped), 0)
		c.Stop()
		c.Stop()
		assert.EqualValues(atomic.LoadUint32(&c.stopped), 1)
		c.Start()
		assert.EqualValues(atomic.LoadUint32(&c.stopped), 0)
		c.Start()
		c.Start()
		assert.EqualValues(atomic.LoadUint32(&c.stopped), 0)
		c.Stop()
		c.Stop()
		assert.EqualValues(atomic.LoadUint32(&c.stopped), 1)
	})
	t.Run("flusher", func(t *testing.T) {
		t.Run("old", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				transport := newDummyTransport()
				c := newConcentrator(newTestConfigWithTransportAndEnv(t, transport, "someEnv"), 500_000, &statsd.NoOpClientDirect{})
				assert.Len(t, transport.Stats(), 0)
				ss1, ok := c.newTracerStatSpan(&s1, nil)
				assert.True(t, ok)
				c.Start()
				c.In <- ss1
				time.Sleep(2 * time.Millisecond) // instant: fake clock advances 2ms past flush interval
				synctest.Wait()                  // wait for concentrator goroutine to flush
				c.Stop()
				actualStats := transport.Stats()
				assert.Len(t, actualStats, 1)
				assert.Len(t, actualStats[0].Stats, 1)
				assert.Len(t, actualStats[0].Stats[0].Stats, 1)
				assert.Equal(t, "http.request", actualStats[0].Stats[0].Stats[0].Name)
			})
		})

		t.Run("recent+stats", func(t *testing.T) {
			transport := newDummyTransport()
			testStats := &statsdtest.TestStatsdClient{}
			c := newConcentrator(newTestConfigWithTransportAndEnv(t, transport, "someEnv"), (10 * time.Second).Nanoseconds(), testStats)
			assert.Len(t, transport.Stats(), 0)
			ss1, ok := c.newTracerStatSpan(&s1, nil)
			assert.True(t, ok)
			ss2, ok := c.newTracerStatSpan(&s2, nil)
			assert.True(t, ok)
			c.Start()
			c.In <- ss1
			c.In <- ss2
			c.Stop()
			actualStats := transport.Stats()
			assert.Len(t, actualStats, 1)
			assert.Len(t, actualStats[0].Stats, 1)
			assert.Len(t, actualStats[0].Stats[0].Stats, 2)
			names := map[string]struct{}{}
			for _, stat := range actualStats[0].Stats[0].Stats {
				names[stat.Name] = struct{}{}
			}
			assert.Len(t, names, 2)
			assert.NotNil(t, names["http.request"])
			assert.NotNil(t, names["potato"])
			assert.Contains(t, testStats.CallNames(), "datadog.tracer.stats.spans_in")
		})

		t.Run("ciGitSha", func(t *testing.T) {
			utils.AddCITags(constants.GitCommitSHA, "DEADBEEF")
			transport := newDummyTransport()
			cfg := newTestConfigWithTransportAndEnv(t, transport, "someEnv")
			cfg.internalConfig.SetCIVisibilityEnabled(true, internalconfig.OriginCode)
			c := newConcentrator(cfg, (10 * time.Second).Nanoseconds(), &statsd.NoOpClientDirect{})
			assert.Len(t, transport.Stats(), 0)
			ss1, ok := c.newTracerStatSpan(&s1, nil)
			assert.True(t, ok)
			c.Start()
			c.In <- ss1
			c.Stop()
			actualStats := transport.Stats()
			assert.Equal(t, "DEADBEEF", actualStats[0].GitCommitSha)
		})

		// stats should be sent if the concentrator is stopped
		t.Run("stop", func(t *testing.T) {
			transport := newDummyTransport()
			c := newConcentrator(newTestConfigWithTransport(t, transport), 500_000, &statsd.NoOpClientDirect{})
			assert.Len(t, transport.Stats(), 0)
			ss1, ok := c.newTracerStatSpan(&s1, nil)
			assert.True(t, ok)
			c.Start()
			c.In <- ss1
			c.Stop()
			assert.NotEmpty(t, transport.Stats())
		})

		t.Run("processTagsEnabled", func(t *testing.T) {
			processtags.Reload()

			transport := newDummyTransport()
			c := newConcentrator(newTestConfigWithTransport(t, transport), 500_000, &statsd.NoOpClientDirect{})
			assert.Len(t, transport.Stats(), 0)
			ss1, ok := c.newTracerStatSpan(&s1, nil)
			assert.True(t, ok)
			c.Start()
			c.In <- ss1
			c.Stop()

			gotStats := transport.Stats()
			require.Len(t, gotStats, 1)
			assert.NotEmpty(t, gotStats[0].ProcessTags)
		})
		t.Run("processTagsDisabled", func(t *testing.T) {
			t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "false")
			processtags.Reload()

			transport := newDummyTransport()
			c := newConcentrator(newTestConfigWithTransport(t, transport), 500_000, &statsd.NoOpClientDirect{})
			assert.Len(t, transport.Stats(), 0)
			ss1, ok := c.newTracerStatSpan(&s1, nil)
			assert.True(t, ok)
			c.Start()
			c.In <- ss1
			c.Stop()

			gotStats := transport.Stats()
			require.Len(t, gotStats, 1)
			assert.Empty(t, gotStats[0].ProcessTags)
		})
	})
}

func TestNewConcentratorAdditionalMetricTagsCardinalityLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		gate  string
		tags  string
		limit string
		want  int
	}{
		{name: "gate off with keys", tags: "customer_id", limit: "7"},
		{name: "gate on without keys", gate: "true", limit: "7"},
		{name: "gate on with keys default limit", gate: "true", tags: "customer_id", want: 100},
		{name: "gate on with keys custom limit", gate: "true", tags: "customer_id", limit: "7", want: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.gate != "" {
				t.Setenv("DD_TRACE_EXPERIMENTAL_FEATURES_ENABLED", tc.gate)
			}
			if tc.tags != "" {
				t.Setenv("DD_TRACE_STATS_ADDITIONAL_TAGS", tc.tags)
			}
			if tc.limit != "" {
				t.Setenv("DD_TRACE_STATS_ADDITIONAL_TAGS_CARDINALITY_LIMIT", tc.limit)
			}

			cfg, err := newTestConfig()
			require.NoError(t, err)
			concentrator := newConcentrator(cfg, defaultStatsBucketSize, &statsd.NoOpClientDirect{})
			assert.Equal(t, tc.want, additionalMetricTagsCardinalityLimit(concentrator))
		})
	}
}

func TestFlushAndSendCollapsedSpansMetric(t *testing.T) {
	t.Setenv("DD_TRACE_EXPERIMENTAL_FEATURES_ENABLED", "true")
	t.Setenv("DD_TRACE_STATS_ADDITIONAL_TAGS", "customer_id,oversized")
	t.Setenv("DD_TRACE_STATS_ADDITIONAL_TAGS_CARDINALITY_LIMIT", "1")

	transport := newDummyTransport()
	testStats := &statsdtest.TestStatsdClient{}
	c := newConcentrator(newTestConfigWithTransportAndEnv(t, transport, "someEnv"), defaultStatsBucketSize, testStats)
	now := time.Now().UnixNano()
	spans := []Span{
		{
			name:     "checkout.process",
			service:  "checkout",
			resource: "POST /checkout",
			start:    now,
			duration: int64(time.Millisecond),
			metrics:  map[string]float64{keyMeasured: 1},
			meta: tinternal.NewSpanMetaFromMap(map[string]string{
				"customer_id": "a",
				"oversized":   strings.Repeat("x", 201),
			}),
		},
		{
			name:     "checkout.process",
			service:  "checkout",
			resource: "POST /checkout",
			start:    now + 1,
			duration: int64(time.Millisecond),
			metrics:  map[string]float64{keyMeasured: 1},
			meta: tinternal.NewSpanMetaFromMap(map[string]string{
				"customer_id": "b",
			}),
		},
	}

	for i := range spans {
		ss, ok := c.newTracerStatSpan(&spans[i], nil)
		require.True(t, ok)
		c.add(ss)
	}

	c.flushAndSend(time.Now(), withCurrentBucket)
	calls := testStats.GetCallsByName("datadog.tracer.stats.collapsed_spans")
	require.Len(t, calls, 2)
	assert.Equal(t, int64(1), testStats.CountCallsByTag(calls, "oversized:additional_metric_tags"))
	assert.Equal(t, int64(1), testStats.CountCallsByTag(calls, "collapsed:additional_metric_tags"))

	testStats.Reset()
	c.flushAndSend(time.Now(), withCurrentBucket)
	assert.Empty(t, testStats.GetCallsByName("datadog.tracer.stats.collapsed_spans"))
}

func TestShouldObfuscate(t *testing.T) {
	bucketSize := int64(500_000)
	tsp := newDummyTransport()
	for _, params := range []struct {
		name                    string
		tracerVersion           int
		agentVersion            int
		expectedShouldObfuscate bool
	}{
		{name: "version equal", tracerVersion: 2, agentVersion: 2, expectedShouldObfuscate: true},
		{name: "agent version missing", tracerVersion: 2, agentVersion: 0, expectedShouldObfuscate: false},
		{name: "agent version older", tracerVersion: 2, agentVersion: 1, expectedShouldObfuscate: true},
		{name: "agent version newer", tracerVersion: 2, agentVersion: 3, expectedShouldObfuscate: false},
	} {
		t.Run(params.name, func(t *testing.T) {
			cfg := newTestConfigWithTransportAndEnv(t, tsp, "someEnv")
			cfg.agent.store(agentFeatures{obfuscationVersion: params.agentVersion})
			c := newConcentrator(cfg, bucketSize, &statsd.NoOpClientDirect{})
			defer func(oldVersion int) { tracerObfuscationVersion = oldVersion }(tracerObfuscationVersion)
			tracerObfuscationVersion = params.tracerVersion
			assert.Equal(t, params.expectedShouldObfuscate, c.shouldObfuscate())
		})
	}
}

func TestObfuscation(t *testing.T) {
	bucketSize := int64(500_000)
	s1 := Span{
		name:     "redis-query",
		start:    time.Now().UnixNano() + 3*bucketSize,
		duration: 1,
		metrics:  map[string]float64{keyMeasured: 1},
		spanType: "redis",
		resource: "GET somekey",
	}
	tsp := newDummyTransport()
	cfg := newTestConfigWithTransportAndEnv(t, tsp, "someEnv")
	af := cfg.agent.load()
	af.obfuscationVersion = 2
	cfg.agent.store(af)
	c := newConcentrator(cfg, bucketSize, &statsd.NoOpClientDirect{})
	defer func(oldVersion int) { tracerObfuscationVersion = oldVersion }(tracerObfuscationVersion)
	tracerObfuscationVersion = 2

	assert.Len(t, tsp.Stats(), 0)
	ss1, ok := c.newTracerStatSpan(&s1, obfuscate.NewObfuscator(obfuscate.Config{}))
	assert.True(t, ok)
	c.Start()
	c.In <- ss1
	c.Stop()
	actualStats := tsp.Stats()
	assert.Len(t, actualStats, 1)
	assert.Len(t, actualStats[0].Stats, 1)
	assert.Len(t, actualStats[0].Stats[0].Stats, 1)
	assert.Equal(t, 2, tsp.obfVersion)
	assert.Equal(t, "GET", actualStats[0].Stats[0].Stats[0].Resource)
}

func TestStatsByKind(t *testing.T) {
	s1 := Span{
		name:     "http.request",
		start:    time.Now().UnixNano(),
		duration: 1,
		metrics:  map[string]float64{keyMeasured: 0},
	}
	s2 := Span{
		name:     "sql.query",
		start:    time.Now().UnixNano(),
		duration: 1,
		metrics:  map[string]float64{keyMeasured: 0},
	}
	s1.SetTag("span.kind", "client")
	s2.SetTag("span.kind", "invalid")

	c := newConcentrator(newTestConfigWithTransport(t, newDummyTransport()), 100, &statsd.NoOpClientDirect{})
	_, ok := c.newTracerStatSpan(&s1, nil)
	assert.True(t, ok)

	_, ok = c.newTracerStatSpan(&s2, nil)
	assert.False(t, ok)
}

func TestConcentratorDefaultEnv(t *testing.T) {
	assert := assert.New(t)

	t.Run("uses-agent-default-env-when-no-tracer-env", func(t *testing.T) {
		cfg, err := newTestConfig(func(c *config) {
			c.ddTransport = newDummyTransport()
		})
		assert.NoError(err)
		af := cfg.agent.load()
		af.defaultEnv = "agent-prod"
		cfg.agent.store(af)
		c := newConcentrator(cfg, 100, &statsd.NoOpClientDirect{})
		assert.Equal("agent-prod", c.aggregationKey.Env)
	})

	t.Run("prefers-tracer-env-over-agent-default", func(t *testing.T) {
		cfg := newTestConfigWithTransportAndEnv(t, newDummyTransport(), "tracer-staging")
		af := cfg.agent.load()
		af.defaultEnv = "agent-prod"
		cfg.agent.store(af)
		c := newConcentrator(cfg, 100, &statsd.NoOpClientDirect{})
		assert.Equal("tracer-staging", c.aggregationKey.Env)
	})

	t.Run("falls-back-to-unknown-env-when-both-empty", func(t *testing.T) {
		cfg := newTestConfigWithTransport(t, newDummyTransport())
		cfg.agent.store(agentFeatures{})
		c := newConcentrator(cfg, 100, &statsd.NoOpClientDirect{})
		assert.Equal("unknown-env", c.aggregationKey.Env)
	})
}

func TestPerSpanVersionInStats(t *testing.T) {
	bucketSize := int64(500_000)
	makeSpan := func(version string) *Span {
		s := &Span{
			name:     "http.request",
			start:    time.Now().UnixNano() + 3*bucketSize,
			duration: 1,
			metrics:  map[string]float64{keyMeasured: 1},
		}
		if version != "" {
			s.meta.Set(ext.Version, version)
		}
		return s
	}

	t.Run("per-span version propagates to stats payload", func(t *testing.T) {
		spanVersion := "synthtracer-20250501120000"
		transport := newDummyTransport()
		c := newConcentrator(newTestConfigWithTransport(t, transport), bucketSize, &statsd.NoOpClientDirect{})

		s := makeSpan(spanVersion)
		ss, ok := c.newTracerStatSpan(s, nil)
		require.True(t, ok)
		c.Start()
		c.In <- ss
		c.Stop()

		got := transport.Stats()
		require.Len(t, got, 1)
		assert.Equal(t, spanVersion, got[0].Version,
			"per-span version tag must be used when no global version is configured")
	})

	t.Run("falls back to global config version when span has no version tag", func(t *testing.T) {
		transport := newDummyTransport()
		cfg, err := newTestConfig(withNoopInfoHTTPClient(), func(c *config) {
			c.ddTransport = transport
			c.internalConfig.SetVersion("global-v1.2.3", internalconfig.OriginCode)
		})
		require.NoError(t, err)
		c := newConcentrator(cfg, bucketSize, &statsd.NoOpClientDirect{})

		s := makeSpan("")
		ss, ok := c.newTracerStatSpan(s, nil)
		require.True(t, ok)
		c.Start()
		c.In <- ss
		c.Stop()

		got := transport.Stats()
		require.Len(t, got, 1)
		assert.Equal(t, "global-v1.2.3", got[0].Version)
	})

	t.Run("two spans with different versions produce separate payloads", func(t *testing.T) {
		transport := newDummyTransport()
		c := newConcentrator(newTestConfigWithTransport(t, transport), bucketSize, &statsd.NoOpClientDirect{})

		s1 := makeSpan("v-timestamp-1")
		s2 := makeSpan("v-timestamp-2")
		ss1, ok := c.newTracerStatSpan(s1, nil)
		require.True(t, ok)
		ss2, ok := c.newTracerStatSpan(s2, nil)
		require.True(t, ok)
		c.Start()
		c.In <- ss1
		c.In <- ss2
		c.Stop()

		got := transport.Stats()
		require.Len(t, got, 2)
		versions := map[string]struct{}{got[0].Version: {}, got[1].Version: {}}
		assert.Contains(t, versions, "v-timestamp-1")
		assert.Contains(t, versions, "v-timestamp-2")
	})
}

func TestStatsIncludeHTTPMethodAndEndpoint(t *testing.T) {
	uniqueMethod := "POST"
	uniqueEndpoint := "/__unique_endpoint__"

	bucketSize := int64(500_000)
	s := Span{
		name:     "http.request",
		start:    time.Now().UnixNano(),
		duration: int64(time.Millisecond),
		metrics:  map[string]float64{keyMeasured: 1},
		meta: tinternal.NewSpanMetaFromMap(map[string]string{
			ext.HTTPMethod:   uniqueMethod,
			ext.HTTPEndpoint: uniqueEndpoint,
		}),
	}
	transport := newDummyTransport()
	c := newConcentrator(newTestConfigWithTransport(t, transport), bucketSize, &statsd.NoOpClientDirect{})
	ss, ok := c.newTracerStatSpan(&s, nil)
	require.True(t, ok)
	c.Start()
	c.In <- ss
	c.Stop()

	actualStats := transport.Stats()
	require.NotEmpty(t, actualStats)

	// Assert via typed fields in the aggregation key
	require.Len(t, actualStats[0].Stats, 1)
	require.NotEmpty(t, actualStats[0].Stats[0].Stats)
	group := actualStats[0].Stats[0].Stats[0]
	assert.Equal(t, uniqueMethod, group.GetHTTPMethod())
	assert.Equal(t, uniqueEndpoint, group.GetHTTPEndpoint())
}

func TestStatsIncludeServiceSource(t *testing.T) {
	bucketSize := int64(500_000)
	s := Span{
		name:          "http.request",
		service:       "custom-service",
		serviceSource: "m",
		start:         time.Now().UnixNano(),
		duration:      int64(time.Millisecond),
		metrics:       map[string]float64{keyMeasured: 1},
		meta: tinternal.NewSpanMetaFromMap(map[string]string{
			ext.KeyServiceSource: "m",
		}),
	}
	transport := newDummyTransport()
	c := newConcentrator(newTestConfigWithTransport(t, transport), bucketSize, &statsd.NoOpClientDirect{})
	ss, ok := c.newTracerStatSpan(&s, nil)
	require.True(t, ok)
	c.Start()
	c.In <- ss
	c.Stop()

	actualStats := transport.Stats()
	require.NotEmpty(t, actualStats)
	require.Len(t, actualStats[0].Stats, 1)
	require.NotEmpty(t, actualStats[0].Stats[0].Stats)
	group := actualStats[0].Stats[0].Stats[0]
	assert.Equal(t, "m", group.GetServiceSource())
}

func TestStatsServiceSourceNotSetWhenEmpty(t *testing.T) {
	bucketSize := int64(500_000)
	s := Span{
		name:     "http.request",
		service:  "my-service",
		start:    time.Now().UnixNano(),
		duration: int64(time.Millisecond),
		metrics:  map[string]float64{keyMeasured: 1},
	}
	transport := newDummyTransport()
	c := newConcentrator(newTestConfigWithTransport(t, transport), bucketSize, &statsd.NoOpClientDirect{})
	ss, ok := c.newTracerStatSpan(&s, nil)
	require.True(t, ok)
	c.Start()
	c.In <- ss
	c.Stop()

	actualStats := transport.Stats()
	require.NotEmpty(t, actualStats)
	require.Len(t, actualStats[0].Stats, 1)
	require.NotEmpty(t, actualStats[0].Stats[0].Stats)
	group := actualStats[0].Stats[0].Stats[0]
	assert.Empty(t, group.GetServiceSource())
}

// failingStatsTransport is a transport whose sendStats fails a configurable
// number of times before succeeding, used to test retry behaviour.
type failingStatsTransport struct {
	dummyTransport
	failCount    int
	sendAttempts int
	statsSent    bool
}

func (t *failingStatsTransport) sendStats(_ *pb.ClientStatsPayload, _ int) error {
	t.sendAttempts++
	if t.failCount > 0 {
		t.failCount--
		return errors.New("stats send failed")
	}
	t.statsSent = true
	return nil
}

func TestStatsFlushRetries(t *testing.T) {
	testcases := []struct {
		configRetries int
		retryInterval time.Duration
		failCount     int
		statsSent     bool
		expAttempts   int
	}{
		{configRetries: 0, retryInterval: time.Millisecond, failCount: 0, statsSent: true, expAttempts: 1},
		{configRetries: 0, retryInterval: time.Millisecond, failCount: 1, statsSent: false, expAttempts: 1},

		{configRetries: 1, retryInterval: time.Millisecond, failCount: 0, statsSent: true, expAttempts: 1},
		{configRetries: 1, retryInterval: time.Millisecond, failCount: 1, statsSent: true, expAttempts: 2},
		{configRetries: 1, retryInterval: time.Millisecond, failCount: 2, statsSent: false, expAttempts: 2},

		{configRetries: 2, retryInterval: time.Millisecond, failCount: 0, statsSent: true, expAttempts: 1},
		{configRetries: 2, retryInterval: time.Millisecond, failCount: 1, statsSent: true, expAttempts: 2},
		{configRetries: 2, retryInterval: time.Millisecond, failCount: 2, statsSent: true, expAttempts: 3},
		{configRetries: 2, retryInterval: time.Millisecond, failCount: 3, statsSent: false, expAttempts: 3},
	}

	bucketSize := int64(500_000)
	s := Span{
		name:     "http.request",
		start:    time.Now().UnixNano() + 3*bucketSize,
		duration: 1,
		metrics:  map[string]float64{keyMeasured: 1},
	}

	for _, test := range testcases {
		name := fmt.Sprintf("retries=%d/fails=%d/sent=%v", test.configRetries, test.failCount, test.statsSent)
		t.Run(name, func(t *testing.T) {
			p := &failingStatsTransport{failCount: test.failCount}
			cfg, err := newTestConfig(func(c *config) {
				c.ddTransport = p
				c.internalConfig.SetSendRetries(test.configRetries, internalconfig.OriginCode)
				c.internalConfig.SetRetryInterval(test.retryInterval, internalconfig.OriginCode)
				c.internalConfig.SetEnv("someEnv", internalconfig.OriginCode)
			})
			require.NoError(t, err)

			c := newConcentrator(cfg, bucketSize, &statsd.NoOpClientDirect{})
			ss, ok := c.newTracerStatSpan(&s, nil)
			require.True(t, ok)
			c.Start()
			c.In <- ss
			c.Stop()

			assert.Equal(t, test.expAttempts, p.sendAttempts)
			assert.Equal(t, test.statsSent, p.statsSent)
		})
	}
}

func TestNoopConcentrator(t *testing.T) {
	var c statsConcentrator = &noopConcentrator{}

	t.Run("Start", func(t *testing.T) {
		assert.NotPanics(t, func() { c.Start() })
	})

	t.Run("Stop", func(t *testing.T) {
		assert.NotPanics(t, func() { c.Stop() })
	})

	t.Run("flushAndSend", func(t *testing.T) {
		assert.NotPanics(t, func() { c.flushAndSend(time.Now(), false) })
	})

	t.Run("newTracerStatSpan", func(t *testing.T) {
		s := &Span{
			name:     "test.op",
			service:  "test-service",
			resource: "/test",
			spanType: "web",
			start:    time.Now().UnixNano(),
			duration: 1,
		}
		ss, ok := c.newTracerStatSpan(s, obfuscate.NewObfuscator(obfuscate.Config{}))
		assert.Nil(t, ss)
		assert.False(t, ok)
	})

	t.Run("trySendSpan", func(t *testing.T) {
		assert.NotPanics(t, func() { c.trySendSpan(&tracerStatSpan{}) })
	})
}
