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

	"github.com/DataDog/datadog-agent/pkg/trace/stats"
)

func TestAlignTs(t *testing.T) {
	now := time.Now().UnixNano()
	got := alignTs(now, defaultStatsBucketSize)
	want := now - now%((10 * time.Second).Nanoseconds())
	assert.Equal(t, got, want)
}

func TestConcentrator(t *testing.T) {
	bucketSize := int64(500_000)

	sc := stats.NewSpanConcentrator(&stats.SpanConcentratorConfig{BucketInterval: bucketSize}, time.Now())
	ss1, ok := sc.NewStatSpan("", "", "http.request", "", 0,
		time.Now().UnixNano()+3*bucketSize,
		1,
		0, nil, map[string]float64{keyMeasured: 1}, nil)
	require.True(t, ok)
	ss2, ok := sc.NewStatSpan("", "", "sql.query", "", 0,
		time.Now().UnixNano()+4*bucketSize,
		1,
		0, nil, map[string]float64{keyMeasured: 1}, nil)
	require.True(t, ok)

	t.Run("start-stop", func(t *testing.T) {
		assert := assert.New(t)
		c := newConcentrator(&config{}, defaultStatsBucketSize)
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

	//t.Run("valid", func(t *testing.T) {
	//	c := newConcentrator(&config{env: "someEnv"}, defaultStatsBucketSize)
	//	//btime := alignTs(ss1.Start+ss1.Duration, defaultStatsBucketSize)
	//	c.add(ss1)
	//
	//	//assert.Len(t, c.buckets, 1)
	//	//b, ok := c.buckets[btime]
	//	//assert.True(t, ok)
	//	//assert.Equal(t, b.start, uint64(btime))
	//	//assert.Equal(t, b.duration, uint64(defaultStatsBucketSize))
	//})

	//t.Run("grouping", func(t *testing.T) {
	//	c := newConcentrator(&config{}, defaultStatsBucketSize)
	//	c.add(ss1)
	//	c.add(ss1)
	//	assert.Len(t, c.buckets, 1)
	//	_, ok := c.buckets[alignTs(ss1.Start+ss1.Duration, defaultStatsBucketSize)]
	//	assert.True(t, ok)
	//	c.add(ss2)
	//	assert.Len(t, c.buckets, 2)
	//	_, ok = c.buckets[alignTs(ss2.Start+ss2.Duration, defaultStatsBucketSize)]
	//	assert.True(t, ok)
	//})
	//
	//t.Run("ingester", func(t *testing.T) {
	//	transport := newDummyTransport()
	//	c := newConcentrator(&config{transport: transport}, defaultStatsBucketSize)
	//	c.Start()
	//	assert.Len(t, c.buckets, 0)
	//	c.In <- ss1
	//	if !waitForBuckets(c, 1) {
	//		t.Fatal("sending to channel did not work")
	//	}
	//	c.Stop()
	//})
	//
	t.Run("flusher", func(t *testing.T) {
		t.Run("old", func(t *testing.T) {
			transport := newDummyTransport()
			c := newConcentrator(&config{transport: transport, env: "someEnv"}, 500_000)
			assert.Len(t, transport.Stats(), 0)
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

		t.Run("recent", func(t *testing.T) {
			transport := newDummyTransport()
			c := newConcentrator(&config{transport: transport, env: "someEnv"}, (10 * time.Second).Nanoseconds())
			assert.Len(t, transport.Stats(), 0)
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
		})
	})
	//
	//	// stats should be sent if the concentrator is stopped
	//	t.Run("stop", func(t *testing.T) {
	//		transport := newDummyTransport()
	//		c := newConcentrator(&config{transport: transport}, 500000)
	//		assert.Len(t, transport.Stats(), 0)
	//		c.Start()
	//		c.In <- &aggregableSpan{
	//			key:      key1,
	//			Start:    time.Now().UnixNano(),
	//			Duration: 1,
	//		}
	//		c.Stop()
	//		assert.NotEmpty(t, transport.Stats())
	//	})
	//})
}
