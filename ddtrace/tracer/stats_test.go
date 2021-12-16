// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// waitForBuckets reports whether concentrator c contains n buckets within a 5ms
// period.
func waitForBuckets(c *concentrator, n int) bool {
	for i := 0; i < 5; i++ {
		time.Sleep(time.Millisecond * timeMultiplicator)
		c.mu.Lock()
		if len(c.buckets) == n {
			return true
		}
		c.mu.Unlock()
	}
	return false
}

func TestAlignTs(t *testing.T) {
	now := time.Now().UnixNano()
	got := alignTs(now, defaultStatsBucketSize)
	want := now - now%((10 * time.Second).Nanoseconds())
	assert.Equal(t, got, want)
}

func TestConcentrator(t *testing.T) {
	key1 := aggregation{
		Name: "http.request",
	}
	ss1 := &aggregableSpan{
		key:      key1,
		Start:    time.Now().UnixNano() + 2*defaultStatsBucketSize,
		Duration: (2 * time.Second).Nanoseconds(),
	}
	key2 := aggregation{
		Name: "sql.query",
	}
	ss2 := &aggregableSpan{
		key:      key2,
		Start:    time.Now().UnixNano() + 3*defaultStatsBucketSize,
		Duration: (3 * time.Second).Nanoseconds(),
	}

	t.Run("new", func(t *testing.T) {
		assert := assert.New(t)
		cfg := &config{version: "1.2.3"}
		c := newConcentrator(cfg, defaultStatsBucketSize)
		assert.Equal(cap(c.In), 10000)
		assert.Nil(c.stop)
		assert.NotNil(c.buckets)
		assert.Equal(c.cfg, cfg)
		assert.EqualValues(c.stopped, 1)
	})

	t.Run("start-stop", func(t *testing.T) {
		assert := assert.New(t)
		c := newConcentrator(&config{}, defaultStatsBucketSize)
		assert.EqualValues(c.stopped, 1)
		c.Start()
		assert.EqualValues(c.stopped, 0)
		c.Stop()
		c.Stop()
		assert.EqualValues(c.stopped, 1)
		c.Start()
		assert.EqualValues(c.stopped, 0)
		c.Start()
		c.Start()
		assert.EqualValues(c.stopped, 0)
		c.Stop()
		c.Stop()
		assert.EqualValues(c.stopped, 1)
	})

	t.Run("valid", func(t *testing.T) {
		c := newConcentrator(&config{}, defaultStatsBucketSize)
		btime := alignTs(ss1.Start+ss1.Duration, defaultStatsBucketSize)
		c.add(ss1)
		assert.Len(t, c.buckets, 1)
		b, ok := c.buckets[btime]
		assert.True(t, ok)
		assert.Equal(t, b.start, uint64(btime))
		assert.Equal(t, b.duration, uint64(defaultStatsBucketSize))
	})

	t.Run("grouping", func(t *testing.T) {
		c := newConcentrator(&config{}, defaultStatsBucketSize)
		c.add(ss1)
		c.add(ss1)
		assert.Len(t, c.buckets, 1)
		_, ok := c.buckets[alignTs(ss1.Start+ss1.Duration, defaultStatsBucketSize)]
		assert.True(t, ok)
		c.add(ss2)
		assert.Len(t, c.buckets, 2)
		_, ok = c.buckets[alignTs(ss2.Start+ss2.Duration, defaultStatsBucketSize)]
		assert.True(t, ok)
	})

	t.Run("ingester", func(t *testing.T) {
		c := newConcentrator(&config{}, defaultStatsBucketSize)
		c.Start()
		assert.Len(t, c.buckets, 0)
		c.In <- ss1
		if !waitForBuckets(c, 1) {
			t.Fatal("sending to channel did not work")
		}
		c.Stop()
	})

	t.Run("flusher", func(t *testing.T) {
		t.Run("old", func(t *testing.T) {
			transport := newDummyTransport()
			c := newConcentrator(&config{transport: transport}, 500000)
			assert.Len(t, transport.Stats(), 0)
			c.Start()
			c.In <- &aggregableSpan{
				key: key2,
				// Start must be older than latest bucket to get flushed
				Start:    time.Now().UnixNano() - 3*500000,
				Duration: 1,
			}
			c.In <- &aggregableSpan{
				key: key2,
				// Start must be older than latest bucket to get flushed
				Start:    time.Now().UnixNano() - 4*500000,
				Duration: 1,
			}
			time.Sleep(2 * time.Millisecond * timeMultiplicator)
			c.Stop()
			assert.NotZero(t, transport.Stats())
		})

		t.Run("recent", func(t *testing.T) {
			transport := newDummyTransport()
			c := newConcentrator(&config{transport: transport}, 500000)
			assert.Len(t, transport.Stats(), 0)
			c.Start()
			c.In <- &aggregableSpan{
				key:      key2,
				Start:    time.Now().UnixNano() + 5*500000,
				Duration: 1,
			}
			c.In <- &aggregableSpan{
				key:      key1,
				Start:    time.Now().UnixNano() + 6*500000,
				Duration: 1,
			}
			c.Stop()
			assert.Zero(t, transport.Stats())
		})
	})
}
