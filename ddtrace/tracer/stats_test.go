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
)

func TestAlignTs(t *testing.T) {
	now := time.Now().UnixNano()
	got := alignTs(now, defaultStatsBucketSize)
	want := now - now%((10 * time.Second).Nanoseconds())
	assert.Equal(t, got, want)
}

func TestConcentrator(t *testing.T) {
	bucketSize := int64(500_000)
	s1 := span{
		Name:     "http.request",
		Start:    time.Now().UnixNano() + 3*bucketSize,
		Duration: 1,
		Metrics:  map[string]float64{keyMeasured: 1},
	}
	s2 := span{
		Name:     "sql.query",
		Start:    time.Now().UnixNano() + 4*bucketSize,
		Duration: 1,
		Metrics:  map[string]float64{keyMeasured: 1},
	}
	t.Run("start-stop", func(t *testing.T) {
		assert := assert.New(t)
		c := newConcentrator(&config{}, bucketSize)
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
			c := newConcentrator(&config{transport: transport, env: "someEnv"}, 500_000)
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

		t.Run("recent", func(t *testing.T) {
			transport := newDummyTransport()
			c := newConcentrator(&config{transport: transport, env: "someEnv"}, (10 * time.Second).Nanoseconds())
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
		})

		// stats should be sent if the concentrator is stopped
		t.Run("stop", func(t *testing.T) {
			transport := newDummyTransport()
			c := newConcentrator(&config{transport: transport}, 500000)
			assert.Len(t, transport.Stats(), 0)
			ss1, ok := c.newTracerStatSpan(&s1, nil)
			assert.True(t, ok)
			c.Start()
			c.In <- ss1
			c.Stop()
			assert.NotEmpty(t, transport.Stats())
		})
	})
}
