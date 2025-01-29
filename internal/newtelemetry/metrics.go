// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"strings"
	"time"

	"go.uber.org/atomic"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/knownmetrics"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

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
func (m *metrics) LoadOrStore(namespace Namespace, kind transport.MetricType, name string, tags map[string]string) MetricHandle {
	if !knownmetrics.IsKnownMetricName(name) {
		log.Debug("telemetry: metric name %q is not a known metric, please update the list of metrics name or check that your wrote the name correctly. "+
			"The metric will still be sent.", name)
	}

	compiledTags := ""
	for k, v := range tags {
		compiledTags += k + ":" + v + ","
	}

	var (
		key    = metricKey{namespace: namespace, kind: kind, name: name, tags: strings.TrimSuffix(compiledTags, ",")}
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
	value      atomic.Float64
	submitTime atomic.Int64
}

func (c *metric) submit() {
	c.newSubmit.Store(true)
	c.submitTime.Store(time.Now().Unix())
}

func (c *metric) payload() transport.MetricData {
	if submit := c.newSubmit.Swap(false); !submit {
		return transport.MetricData{}
	}

	var tags []string
	if c.key.tags != "" {
		tags = strings.Split(c.key.tags, ",")
	}

	data := transport.MetricData{
		Metric:    c.key.name,
		Namespace: c.key.namespace,
		Tags:      tags,
		Type:      c.key.kind,
		Common:    knownmetrics.IsCommonMetricName(c.key.name),
		Points: [][2]any{
			{c.submitTime.Load(), c.value.Load()},
		},
	}

	return data
}

type count struct {
	metric
}

func (c *count) Submit(value float64) {
	c.submit()
	c.value.Add(value)
}

type gauge struct {
	metric
}

func (c *gauge) Submit(value float64) {
	c.submit()
	c.value.Store(value)
}

type rate struct {
	metric
	intervalStart time.Time
}

func (c *rate) Submit(value float64) {
	c.submit()
	c.value.Add(value)
}

func (c *rate) payload() transport.MetricData {
	payload := c.metric.payload()
	if payload.Metric == "" {
		return payload
	}

	payload.Interval = int64(time.Since(c.intervalStart).Seconds())
	payload.Points[0][1] = c.value.Load() / float64(payload.Interval)
	return payload
}
