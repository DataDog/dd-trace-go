// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"math"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/knownmetrics"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

// metricKey is used as a key in the metrics store hash map.
type metricKey struct {
	namespace Namespace
	kind      transport.MetricType
	name      string
	tags      string
}

// metricsHandle is the internal equivalent of MetricHandle for Count/Rate/Gauge metrics that are sent via the payload [transport.GenerateMetrics].
type metricHandle interface {
	MetricHandle
	payload() transport.MetricData
}

type metrics struct {
	store         internal.TypedSyncMap[metricKey, metricHandle]
	skipAllowlist bool // Debugging feature to skip the allowlist of known metrics
}

// LoadOrStore returns a MetricHandle for the given metric key. If the metric key does not exist, it will be created.
func (m *metrics) LoadOrStore(namespace Namespace, kind transport.MetricType, name string, tags []string) MetricHandle {
	if !knownmetrics.IsKnownMetric(namespace, string(kind), name) {
		log.Debug("telemetry: metric name %q is not a known metric, please update the list of metrics name or check that your wrote the name correctly. "+
			"The metric will still be sent.", name)
	}

	var (
		key    = metricKey{namespace: namespace, kind: kind, name: name, tags: strings.Join(tags, ",")}
		handle MetricHandle
	)
	switch kind {
	case transport.CountMetric:
		handle, _ = m.store.LoadOrStore(key, &count{metric: metric{key: key}})
	case transport.GaugeMetric:
		handle, _ = m.store.LoadOrStore(key, &gauge{metric: metric{key: key}})
	case transport.RateMetric:
		handle, _ = m.store.LoadOrStore(key, &rate{metric: metric{key: key}, intervalStart: time.Now()})
	}

	return handle
}

func (m *metrics) Payload() transport.Payload {
	series := make([]transport.MetricData, 0, m.store.Len())
	m.store.Range(func(_ metricKey, handle metricHandle) bool {
		if payload := handle.payload(); payload.Metric != "" {
			series = append(series, payload)
		}
		return true
	})

	if len(series) == 0 {
		return nil
	}

	return transport.GenerateMetrics{Series: series, SkipAllowlist: m.skipAllowlist}
}

type metric struct {
	key metricKey

	// Values set during Submit()
	newSubmit  atomic.Bool
	value      atomic.Uint64 // Actually a float64, but we need atomic operations
	submitTime atomic.Int64  // Unix timestamp
}

func (m *metric) Get() float64 {
	return math.Float64frombits(m.value.Load())
}

func (m *metric) payload() transport.MetricData {
	if submit := m.newSubmit.Swap(false); !submit {
		return transport.MetricData{}
	}

	var tags []string
	if m.key.tags != "" {
		tags = strings.Split(m.key.tags, ",")
	}

	data := transport.MetricData{
		Metric:    m.key.name,
		Namespace: m.key.namespace,
		Tags:      tags,
		Type:      m.key.kind,
		Common:    knownmetrics.IsCommonMetric(m.key.namespace, string(m.key.kind), m.key.name),
		Points: [][2]any{
			{m.submitTime.Load(), math.Float64frombits(m.value.Load())},
		},
	}

	return data
}

type count struct {
	metric
}

func (c *count) Submit(value float64) {
	c.newSubmit.Store(true)
	c.submitTime.Store(time.Now().Unix())
	for {
		oldValue := c.Get()
		newValue := oldValue + value
		if c.value.CompareAndSwap(math.Float64bits(oldValue), math.Float64bits(newValue)) {
			return
		}
	}
}

type gauge struct {
	metric
}

func (g *gauge) Submit(value float64) {
	g.newSubmit.Store(true)
	g.submitTime.Store(time.Now().Unix())
	g.value.Store(math.Float64bits(value))
}

type rate struct {
	metric
	intervalStart time.Time
}

func (r *rate) Submit(value float64) {
	r.newSubmit.Store(true)
	r.submitTime.Store(time.Now().Unix())
	for {
		oldValue := r.Get()
		newValue := oldValue + value
		if r.value.CompareAndSwap(math.Float64bits(oldValue), math.Float64bits(newValue)) {
			return
		}
	}
}

func (r *rate) payload() transport.MetricData {
	payload := r.metric.payload()
	if payload.Metric == "" {
		return payload
	}

	payload.Interval = int64(time.Since(r.intervalStart).Seconds())
	payload.Points[0][1] = r.Get() / float64(payload.Interval)
	return payload
}
