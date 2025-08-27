// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/datadog-agent/pkg/obfuscate"
	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
)

func TestAlignTs(t *testing.T) {
	now := time.Now().UnixNano()
	got := alignTs(now, defaultStatsBucketSize)
	want := now - now%((10 * time.Second).Nanoseconds())
	assert.Equal(t, got, want)
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
		c := newConcentrator(&config{}, bucketSize, &statsd.NoOpClientDirect{})
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
			transport := newDummyTransport()
			c := newConcentrator(&config{transport: transport, env: "someEnv"}, 500_000, &statsd.NoOpClientDirect{})
			assert.Len(t, transport.Stats(), 0)
			ss1, ok := c.newTracerStatSpan(&s1, nil)
			assert.True(t, ok)
			c.Start()
			c.In <- ss1
			time.Sleep(2 * time.Millisecond * timeMultiplicator)
			c.Stop()
			actualStats := transport.Stats()
			assert.Len(t, actualStats, 1)
			assert.Len(t, actualStats[0].Stats, 1)
			assert.Len(t, actualStats[0].Stats[0].Stats, 1)
			assert.Equal(t, "http.request", actualStats[0].Stats[0].Stats[0].Name)
		})

		t.Run("recent+stats", func(t *testing.T) {
			transport := newDummyTransport()
			testStats := &statsdtest.TestStatsdClient{}
			c := newConcentrator(&config{transport: transport, env: "someEnv"}, (10 * time.Second).Nanoseconds(), testStats)
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
			c := newConcentrator(&config{transport: transport, env: "someEnv", ciVisibilityEnabled: true}, (10 * time.Second).Nanoseconds(), &statsd.NoOpClientDirect{})
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
			c := newConcentrator(&config{transport: transport}, 500_000, &statsd.NoOpClientDirect{})
			assert.Len(t, transport.Stats(), 0)
			ss1, ok := c.newTracerStatSpan(&s1, nil)
			assert.True(t, ok)
			c.Start()
			c.In <- ss1
			c.Stop()
			assert.NotEmpty(t, transport.Stats())
		})

		t.Run("processTagsEnabled", func(t *testing.T) {
			t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "true")
			processtags.Reload()

			transport := newDummyTransport()
			c := newConcentrator(&config{transport: transport}, 500_000, &statsd.NoOpClientDirect{})
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
			c := newConcentrator(&config{transport: transport}, 500_000, &statsd.NoOpClientDirect{})
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
			c := newConcentrator(&config{transport: tsp, env: "someEnv", agent: agentFeatures{obfuscationVersion: params.agentVersion}}, bucketSize, &statsd.NoOpClientDirect{})
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
	c := newConcentrator(&config{transport: tsp, env: "someEnv", agent: agentFeatures{obfuscationVersion: 2}}, bucketSize, &statsd.NoOpClientDirect{})
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

	c := newConcentrator(&config{transport: newDummyTransport(), env: "someEnv"}, 100, &statsd.NoOpClientDirect{})
	_, ok := c.newTracerStatSpan(&s1, nil)
	assert.True(t, ok)

	_, ok = c.newTracerStatSpan(&s2, nil)
	assert.False(t, ok)
}

func TestConcentratorDefaultEnv(t *testing.T) {
	assert := assert.New(t)

	t.Run("uses-agent-default-env-when-no-tracer-env", func(t *testing.T) {
		cfg := &config{
			transport: newDummyTransport(),
			agent:     agentFeatures{defaultEnv: "agent-prod"},
		}
		c := newConcentrator(cfg, 100, &statsd.NoOpClientDirect{})
		assert.Equal("agent-prod", c.aggregationKey.Env)
	})

	t.Run("prefers-tracer-env-over-agent-default", func(t *testing.T) {
		cfg := &config{
			transport: newDummyTransport(),
			env:       "tracer-staging",
			agent:     agentFeatures{defaultEnv: "agent-prod"},
		}
		c := newConcentrator(cfg, 100, &statsd.NoOpClientDirect{})
		assert.Equal("tracer-staging", c.aggregationKey.Env)
	})

	t.Run("falls-back-to-unknown-env-when-both-empty", func(t *testing.T) {
		cfg := &config{
			transport: newDummyTransport(),
			agent:     agentFeatures{},
		}
		c := newConcentrator(cfg, 100, &statsd.NoOpClientDirect{})
		assert.Equal("unknown-env", c.aggregationKey.Env)
	})
}
