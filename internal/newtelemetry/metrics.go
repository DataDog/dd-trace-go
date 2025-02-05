// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
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

func (k metricKey) SplitTags() []string {
	if k.tags == "" {
		return nil
	}
	return strings.Split(k.tags, ",")
}

func validateMetricKey(namespace Namespace, kind transport.MetricType, name string, tags []string) error {
	if len(name) == 0 {
		return fmt.Errorf("telemetry: metric name with tags %v should be empty", tags)
	}

	if !knownmetrics.IsKnownMetric(namespace, kind, name) {
		return fmt.Errorf("telemetry: metric name %q of kind %q in namespace %q is not a known metric, please update the list of metrics name or check that your wrote the name correctly. "+
			"The metric will still be sent", name, string(kind), namespace)
	}

	for _, tag := range tags {
		if len(tag) == 0 {
			return fmt.Errorf("telemetry: metric %q has should not have empty tags", name)
		}

		if strings.Contains(tag, ",") {
			return fmt.Errorf("telemetry: metric %q tag %q should not contain commas", name, tag)
		}
	}

	return nil
}

// newMetricKey returns a new metricKey with the given parameters with the tags sorted and joined by commas.
func newMetricKey(namespace Namespace, kind transport.MetricType, name string, tags []string) metricKey {
	sort.Strings(tags)
	return metricKey{namespace: namespace, kind: kind, name: name, tags: strings.Join(tags, ",")}
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

	var (
		key    = newMetricKey(namespace, kind, name, tags)
		handle MetricHandle
		loaded bool
	)
	switch kind {
	case transport.CountMetric:
		handle, loaded = m.store.LoadOrStore(key, &count{metric: metric{key: key}})
	case transport.GaugeMetric:
		handle, loaded = m.store.LoadOrStore(key, &gauge{metric: metric{key: key}})
	case transport.RateMetric:
		handle, loaded = m.store.LoadOrStore(key, &rate{metric: metric{key: key}})
		if !loaded {
			// Initialize the interval start for rate metrics
			r := handle.(*rate)
			now := time.Now()
			r.intervalStart.Store(&now)
		}
	default:
		log.Warn("telemetry: unknown metric type %q", kind)
		return nil
	}

	if !loaded { // The metric is new: validate and log issues about it
		if err := validateMetricKey(namespace, kind, name, tags); err != nil {
			log.Warn("telemetry: %v", err)
		}
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

type metricPoint struct {
	value float64
	time  time.Time
}

var metricPointPool = sync.Pool{
	New: func() any {
		return &metricPoint{value: math.NaN()}
	},
}

type metric struct {
	key           metricKey
	ptr           atomic.Pointer[metricPoint]
	intervalStart atomic.Pointer[time.Time]
}

func (m *metric) Get() float64 {
	if ptr := m.ptr.Load(); ptr != nil {
		return ptr.value
	}

	return math.NaN()
}

func (m *metric) payload() transport.MetricData {
	point := m.ptr.Swap(nil)
	if point == nil {
		return transport.MetricData{}
	}

	var (
		value           = point.value
		intervalStart   = m.intervalStart.Swap(nil)
		intervalSeconds float64
	)

	// Rate metric only
	if intervalStart != nil {
		intervalSeconds = time.Since(*intervalStart).Seconds()
		if int64(intervalSeconds) == 0 { // Interval for rate is too small, we prefer not sending data over sending something wrong
			return transport.MetricData{}
		}

		value = value / intervalSeconds
	}

	return transport.MetricData{
		Metric:    m.key.name,
		Namespace: m.key.namespace,
		Tags:      m.key.SplitTags(),
		Type:      m.key.kind,
		Common:    knownmetrics.IsCommonMetric(m.key.namespace, m.key.kind, m.key.name),
		Interval:  int64(intervalSeconds),
		Points: [][2]any{
			{point.time.Unix(), value},
		},
	}
}

type count struct {
	metric
}

func (m *count) Submit(newValue float64) {
	newPoint := metricPointPool.Get().(*metricPoint)
	newPoint.time = time.Now()
	for {
		oldPoint := m.ptr.Load()
		var oldValue float64
		if oldPoint != nil {
			oldValue = oldPoint.value
		}
		newPoint.value = oldValue + newValue
		if m.ptr.CompareAndSwap(oldPoint, newPoint) {
			if oldPoint != nil {
				metricPointPool.Put(oldPoint)
			}
			return
		}
	}
}

type gauge struct {
	metric
}

func (m *gauge) Submit(value float64) {
	newPoint := metricPointPool.Get().(*metricPoint)
	newPoint.time = time.Now()
	newPoint.value = value
	for {
		oldPoint := m.ptr.Load()
		if m.ptr.CompareAndSwap(oldPoint, newPoint) {
			if oldPoint != nil {
				metricPointPool.Put(oldPoint)
			}
			return
		}
	}
}

type rate struct {
	metric
}

func (m *rate) Submit(newValue float64) {
	newPoint := metricPointPool.Get().(*metricPoint)
	newPoint.time = time.Now()
	for {
		oldPoint := m.ptr.Load()
		var oldValue float64
		if oldPoint != nil {
			oldValue = oldPoint.value
		}
		newPoint.value = oldValue + newValue
		if m.ptr.CompareAndSwap(oldPoint, newPoint) {
			if oldPoint != nil {
				metricPointPool.Put(oldPoint)
			}
			return
		}
	}
}
