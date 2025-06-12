// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package statsdtest

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTestStatsdClient(t *testing.T) {

	t.Run("gauge", func(t *testing.T) {
		var tg TestStatsdClient
		tg.Gauge("name", 2, []string{}, 1)
		tg.Gauge("name2", 5, []string{}, 1)
		tg.Gauge("name3", 1, []string{}, 1)
		tg.Gauge("name", 3, []string{}, 1)

		calls := tg.ValsByName()
		assert.Equal(t, float64(5), calls["name"])
		assert.Equal(t, float64(5), calls["name2"])
		assert.Equal(t, float64(1), calls["name3"])
	})

	t.Run("gauge with timestamp", func(t *testing.T) {
		//var tg TestStatsdClient
	})

	t.Run("incr", func(t *testing.T) {
		var tg TestStatsdClient
		for range 5 {
			tg.Incr("name", []string{}, 1)
		}

		assert.Equal(t, 5, tg.n)
		assert.Equal(t, int64(5), tg.counts["name"])
	})

	t.Run("count", func(t *testing.T) {
		var tg TestStatsdClient
		tg.Count("name", 2, []string{}, 1)
		tg.Count("name2", 5, []string{}, 1)
		tg.Count("name3", 1, []string{}, 1)
		tg.Count("name", 3, []string{}, 1)

		assert.Equal(t, int64(5), tg.counts["name"])
		assert.Equal(t, int64(5), tg.counts["name2"])
		assert.Equal(t, int64(1), tg.counts["name3"])

		assert.Equal(t, 4, tg.n)

	})

	t.Run("count with timestamp", func(t *testing.T) {
		//var tg TestStatsdClient
	})

	t.Run("timing", func(t *testing.T) {
		//var tg TestStatsdClient
	})

	t.Run("reset", func(t *testing.T) {
		var tg TestStatsdClient
		tg.Count("name", 2, []string{}, 1)
		tg.Gauge("name2", 5, []string{}, 1)
		tg.Incr("name3", []string{}, 1)

		tg.Reset()
		assert.Equal(t, tg.n, 0)
		assert.Len(t, tg.counts, 0)
		assert.Len(t, tg.gaugeCalls, 0)
		assert.Len(t, tg.incrCalls, 0)
	})

}

func TestTestStatsdClientConcurrent(t *testing.T) {
	t.Run("gauge", func(t *testing.T) {
		//var tg TestStatsdClient
	})

	t.Run("gauge with timestamp", func(t *testing.T) {
		//var tg TestStatsdClient
	})

	t.Run("incr", func(t *testing.T) {
		assert := assert.New(t)
		var tg TestStatsdClient

		wg := sync.WaitGroup{}
		for range 100 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tg.Incr("name", []string{"tag"}, 1)
			}()
		}
		wg.Wait()
		tg.Wait(assert, 100, 100*time.Millisecond)

		assert.Equal(int64(100), tg.counts["name"])
	})

	t.Run("count", func(t *testing.T) {
		assert := assert.New(t)
		var tg TestStatsdClient

		wg := sync.WaitGroup{}
		for range 100 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tg.Count("name", 1, []string{"tag"}, 1)
			}()
		}
		wg.Wait()
		tg.Wait(assert, 100, 100*time.Millisecond)
		assert.Equal(100, tg.n)
		assert.Equal(int64(100), tg.Counts()["name"])

	})

	t.Run("count with timestamp", func(t *testing.T) {
		//var tg TestStatsdClient
	})

	t.Run("timing", func(t *testing.T) {
		//var tg TestStatsdClient
	})

	t.Run("multi", func(t *testing.T) {
		assert := assert.New(t)
		var tg TestStatsdClient

		wg := sync.WaitGroup{}
		for range 100 {
			wg.Add(3)
			go func() {
				defer wg.Done()
				tg.Count("count", 1, []string{}, 1)
				tg.Count("count2", 3, []string{}, 1)
			}()

			go func() {
				defer wg.Done()
				tg.Timing("timing", time.Nanosecond, []string{}, 1)
			}()

			go func() {
				defer wg.Done()
				tg.Incr("count", []string{}, 1)
			}()

		}
		wg.Wait()
		tg.Wait(assert, 400, 200*time.Millisecond)
		assert.Equal(400, tg.n)
		counts := tg.Counts()
		assert.Equal(int64(200), counts["count"])
		assert.Equal(int64(300), counts["count2"])

		timings := tg.TimingCalls()
		for _, v := range timings {
			assert.Equal("timing", v.name)
			assert.Equal(time.Nanosecond, v.timeVal)
		}
	})

}
