// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package telemetry

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/puzpuzpuz/xsync/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

func newTestMetrics() *metrics {
	return &metrics{store: xsync.NewMap[metricKey, metricHandle]()}
}

// TestGaugeInterval checks that a gauge payload reports the whole seconds elapsed
// since the previous flush. The generate-metrics schema requires this interval on
// every gauge series. The test also checks that the client sends no series when
// less than one second elapsed.
func TestGaugeInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestMetrics()
		g := m.LoadOrStore(NamespaceTracers, transport.GaugeMetric, "init_time", nil).(*gauge)
		g.Submit(1)

		// Less than one whole second elapsed since handle creation. The client
		// sends no series rather than a series with a wrong interval.
		assert.Empty(t, g.Payload().Type)

		// The first payload reports the interval since handle creation. The
		// payload does not change the value.
		time.Sleep(2 * time.Second)
		payload := g.Payload()
		require.Equal(t, transport.GaugeMetric, payload.Type)
		assert.Equal(t, int64(2), payload.Interval)
		require.Len(t, payload.Points, 1)
		assert.Equal(t, 1.0, payload.Points[0][1])

		// The next payload reports the interval that elapsed since the previous
		// flush.
		g.Submit(3)
		time.Sleep(3 * time.Second)
		payload = g.Payload()
		assert.Equal(t, int64(3), payload.Interval)
		assert.Equal(t, 3.0, payload.Points[0][1])
	})
}

// TestGaugeSubSecondFlushInterval checks that a sub-second flush interval does
// not reset the interval start. The client skips the flush at half a second.
// The client sends the series at the next flush with a one-second interval.
func TestGaugeSubSecondFlushInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestMetrics()
		g := m.LoadOrStore(NamespaceTracers, transport.GaugeMetric, "init_time", nil).(*gauge)
		g.Submit(1)

		time.Sleep(500 * time.Millisecond)
		assert.Empty(t, g.Payload().Type)

		time.Sleep(500 * time.Millisecond)
		payload := g.Payload()
		require.Equal(t, transport.GaugeMetric, payload.Type)
		assert.Equal(t, int64(1), payload.Interval)
		assert.Equal(t, 1.0, payload.Points[0][1])
	})
}

// TestRateSubSecondFlushInterval checks that a sub-second flush interval does
// not reset the interval start. The client skips the flush at half a second.
// The client sends the series at the next flush with a one-second interval.
func TestRateSubSecondFlushInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestMetrics()
		r := m.LoadOrStore(NamespaceTracers, transport.RateMetric, "init_time", nil).(*rate)
		r.Submit(2)

		time.Sleep(500 * time.Millisecond)
		assert.Empty(t, r.Payload().Type)

		time.Sleep(500 * time.Millisecond)
		payload := r.Payload()
		require.Equal(t, transport.RateMetric, payload.Type)
		assert.Equal(t, int64(1), payload.Interval)
		assert.Equal(t, 2.0, payload.Points[0][1])
	})
}

// TestCountPayloadNoInterval checks that a count payload omits the interval. The
// generate-metrics schema does not require the interval for count metrics.
func TestCountPayloadNoInterval(t *testing.T) {
	m := newTestMetrics()
	c := m.LoadOrStore(NamespaceTracers, transport.CountMetric, "init_time", nil).(*count)
	c.Submit(1)

	payload := c.Payload()
	require.Equal(t, transport.CountMetric, payload.Type)
	assert.Zero(t, payload.Interval)
	require.Len(t, payload.Points, 1)
	assert.Equal(t, 1.0, payload.Points[0][1])
}
