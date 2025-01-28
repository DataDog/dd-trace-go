// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"strings"
	"time"

	"go.uber.org/atomic"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/knownmetrics"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
)

type metricKey struct {
	namespace types.Namespace
	kind      transport.MetricType
	name      string
	tags      string
}

type metrics struct {
	store         internal.TypedSyncMap[metricKey, MetricHandle]
	skipAllowlist bool // Debugging feature to skip the allowlist of known metrics
}

// LoadOrStore returns a MetricHandle for the given metric key. If the metric key does not exist, it will be created.
func (m *metrics) LoadOrStore(namespace types.Namespace, kind transport.MetricType, name string, tags map[string]string) MetricHandle {
	compiledTags := ""
	for k, v := range tags {
		compiledTags += k + ":" + v + ","
	}
	key := metricKey{namespace: namespace, kind: kind, name: name, tags: strings.TrimSuffix(compiledTags, ",")}
	handle, _ := m.store.LoadOrStore(key, newMetric(key))
	return handle
}

func newMetric(key metricKey) MetricHandle {
	switch key.kind {
	case transport.CountMetric:
		return &count{key: key}
	default:
		panic("unsupported metric type: " + key.kind)
	}
}

func (m *metrics) Payload() transport.Payload {
	series := make([]transport.MetricData, 0, m.store.Len())
	m.store.Range(func(_ metricKey, handle MetricHandle) bool {
		if payload := handle.payload(); payload.Metric != "" {
			series = append(series, payload)
		}
		return true
	})
	return transport.GenerateMetrics{Series: series, SkipAllowlist: m.skipAllowlist}
}

type count struct {
	key       metricKey
	submit    atomic.Bool
	value     atomic.Float64
	timestamp atomic.Int64
}

func (c *count) Submit(value float64) {
	// There is kind-of a race condition here, but it's not a big deal, as the value and the timestamp will be sufficiently close together
	c.submit.Store(true)
	c.value.Add(value)
	c.timestamp.Store(time.Now().Unix())
}

func (c *count) payload() transport.MetricData {
	if submit := c.submit.Swap(false); !submit {
		return transport.MetricData{}
	}

	return transport.MetricData{
		Metric:    c.key.name,
		Namespace: c.key.namespace,
		Tags:      strings.Split(c.key.tags, ","),
		Type:      c.key.kind,
		Common:    knownmetrics.IsCommonMetricName(c.key.name),
		Points: [][2]any{
			{c.timestamp.Load(), c.value.Load()},
		},
	}
}
