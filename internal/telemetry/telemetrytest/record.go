// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetrytest

import (
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/knownmetrics"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

type MetricKey struct {
	Namespace telemetry.Namespace
	Name      string
	Tags      string
	Kind      string
}

type LogLine struct {
	Level telemetry.LogLevel
	Text  string
}

type RecordClient struct {
	mu            sync.Mutex
	Started       bool
	Stopped       bool
	Configuration []telemetry.Configuration
	Logs          []LogLine
	Integrations  []telemetry.Integration
	Products      map[telemetry.Namespace]bool
	Metrics       map[MetricKey]*RecordMetricHandle
	knownMetrics  bool
}

func (r *RecordClient) Close() error {
	return nil
}

type RecordMetricHandle struct {
	mu        sync.Mutex
	count     float64
	rate      float64
	rateStart time.Time
	gauge     float64
	distrib   []float64

	submit func(handle *RecordMetricHandle, value float64)
	get    func(handle *RecordMetricHandle) float64
}

func (m *RecordMetricHandle) Submit(value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submit(m, value)
}

func (m *RecordMetricHandle) Get() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.get(m)
}

func (r *RecordClient) metric(kind string, namespace telemetry.Namespace, name string, tags []string, submit func(handle *RecordMetricHandle, value float64), get func(handle *RecordMetricHandle) float64) *RecordMetricHandle {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.knownMetrics && !knownmetrics.IsKnownMetric(namespace, transport.MetricType(kind), name) {
		panic("telemetrytest.RecordClient should only be used with backend-side known metrics")
	}

	if r.Metrics == nil {
		r.Metrics = make(map[MetricKey]*RecordMetricHandle)
	}

	sort.Strings(tags)
	key := MetricKey{Namespace: namespace, Name: name, Tags: strings.Join(tags, ","), Kind: kind}
	if _, ok := r.Metrics[key]; !ok {
		r.Metrics[key] = &RecordMetricHandle{submit: submit, get: get}
	}
	return r.Metrics[key]
}

func (r *RecordClient) Count(namespace telemetry.Namespace, name string, tags []string) telemetry.MetricHandle {
	return r.metric(string(transport.CountMetric), namespace, name, tags, func(handle *RecordMetricHandle, value float64) {
		handle.count += value
	}, func(handle *RecordMetricHandle) float64 {
		return handle.count
	})
}

func (r *RecordClient) Rate(namespace telemetry.Namespace, name string, tags []string) telemetry.MetricHandle {
	handle := r.metric(string(transport.RateMetric), namespace, name, tags, func(handle *RecordMetricHandle, value float64) {
		handle.count += value
		handle.rate = float64(handle.count) / time.Since(handle.rateStart).Seconds()
	}, func(handle *RecordMetricHandle) float64 {
		return handle.rate
	})

	handle.rateStart = time.Now()
	return handle
}

func (r *RecordClient) Gauge(namespace telemetry.Namespace, name string, tags []string) telemetry.MetricHandle {
	return r.metric(string(transport.GaugeMetric), namespace, name, tags, func(handle *RecordMetricHandle, value float64) {
		handle.gauge = value
	}, func(handle *RecordMetricHandle) float64 {
		return handle.gauge
	})
}

func (r *RecordClient) Distribution(namespace telemetry.Namespace, name string, tags []string) telemetry.MetricHandle {
	return r.metric(string(transport.DistMetric), namespace, name, tags, func(handle *RecordMetricHandle, value float64) {
		handle.distrib = append(handle.distrib, value)
	}, func(handle *RecordMetricHandle) float64 {
		var sum float64
		for _, v := range handle.distrib {
			sum += v
		}
		return sum
	})
}

func (r *RecordClient) Log(level telemetry.LogLevel, text string, _ ...telemetry.LogOption) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Logs = append(r.Logs, LogLine{
		Level: level,
		Text:  text,
	})
}

func (r *RecordClient) ProductStarted(product telemetry.Namespace) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Products == nil {
		r.Products = make(map[telemetry.Namespace]bool)
	}

	r.Products[product] = true
}

func (r *RecordClient) ProductStopped(product telemetry.Namespace) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Products == nil {
		r.Products = make(map[telemetry.Namespace]bool)
	}

	r.Products[product] = false
}

func (r *RecordClient) ProductStartError(product telemetry.Namespace, _ error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Products == nil {
		r.Products = make(map[telemetry.Namespace]bool)
	}

	r.Products[product] = false
}

func (r *RecordClient) RegisterAppConfig(key string, value any, origin telemetry.Origin) {
	r.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: value, Origin: origin})
}

func (r *RecordClient) RegisterAppConfigs(kvs ...telemetry.Configuration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range kvs {
		kvs[i].Value = telemetry.SanitizeConfigValue(kvs[i].Value)
	}
	r.Configuration = append(r.Configuration, kvs...)
}

func (r *RecordClient) MarkIntegrationAsLoaded(integration telemetry.Integration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Integrations = append(r.Integrations, integration)
}

func (r *RecordClient) Flush() {}

func (r *RecordClient) AppStart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Started = true
}

func (r *RecordClient) AppStop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Stopped = true
}

func (r *RecordClient) AddFlushTicker(func(telemetry.Client)) {
}

func CheckConfig(t *testing.T, cfgs []telemetry.Configuration, key string, value any) {
	t.Helper()
	for _, c := range cfgs {
		if c.Name == key && reflect.DeepEqual(c.Value, value) {
			return
		}
	}

	t.Fatalf("could not find configuration key %s with value %v", key, value)
}
