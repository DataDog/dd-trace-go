// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import "github.com/DataDog/dd-trace-go/v2/internal/telemetry"

// TelemetryMetric is a handle to a telemetry metric.
type TelemetryMetric = telemetry.MetricHandle

// TelemetryMetrics is the interface used to submit instrumentation telemetry
// metrics data.
//
// IMPORTANT: If you are not sure what this is for, you should probably be using
// [StatsdClient] instead.
type TelemetryMetrics interface {
	// Count obtains a [TelemetryMetric] for a counter (representing the sum of
	// submitted values for a given interval).
	Count(ns telemetry.Namespace, name string, tags []string) TelemetryMetric
	// Rate obtains a [TelemetryMetric] for a rate metric (representing the value
	// per interval).
	Rate(ns telemetry.Namespace, name string, tags []string) TelemetryMetric
	// Count obtains a [TelemetryMetric] for a gauge (representing the last
	// submitted value for a given interval).
	Gauge(ns telemetry.Namespace, name string, tags []string) TelemetryMetric
	// Distribution obtains a [TelemetryMetric] for a distribution metric
	// (representing the distribution of submitted values for a given interval).
	Distribution(ns telemetry.Namespace, name string, tags []string) TelemetryMetric
}

// TelemetryMetricsClient is the interface used to manage instrumentation
// telemetry metrics data.
//
// IMPORTANT: If you are not sure what this is for, you should probably be using
// [StatsdClient] instead.
type TelemetryMetricsClient interface {
	TelemetryMetrics
	// OnHeartbeat registers a function to be called at each time the telemetry
	// client flushes metrics. This is useful for emitting heartbeat metrics.
	OnHeartbeat(ticket func(TelemetryMetrics))
}

type telemetryMetrics struct {
	// valid is a marker to ensure thr zero-value is not usable.
	valid bool
}

func (t *telemetryMetrics) Count(ns telemetry.Namespace, name string, tags []string) TelemetryMetric {
	t.assertValid()
	return telemetry.Count(ns, name, tags)
}

func (t *telemetryMetrics) Rate(ns telemetry.Namespace, name string, tags []string) TelemetryMetric {
	t.assertValid()
	return telemetry.Count(ns, name, tags)
}

func (t *telemetryMetrics) Gauge(ns telemetry.Namespace, name string, tags []string) TelemetryMetric {
	t.assertValid()
	return telemetry.Count(ns, name, tags)
}

func (t *telemetryMetrics) Distribution(ns telemetry.Namespace, name string, tags []string) TelemetryMetric {
	t.assertValid()
	return telemetry.Count(ns, name, tags)
}

func (t *telemetryMetrics) OnHeartbeat(ticker func(TelemetryMetrics)) {
	t.assertValid()
	telemetry.AddFlushTicker(func(client telemetry.Client) { ticker(client) })
}

// assertValid panics if the [*telemetryMetrics] is a zero-value.
func (t *telemetryMetrics) assertValid() {
	if !t.valid {
		panic("TelemetryMetricsClient must be obtained from Instrumentation.TelemetryMetrics()")
	}
}
