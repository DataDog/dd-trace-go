// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"strings"
	"sync"
	"sync/atomic"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/knownmetrics"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

type distributionKey struct {
	namespace Namespace
	name      string
	tags      string
}

type distributions struct {
	store         internal.TypedSyncMap[distributionKey, *distribution]
	skipAllowlist bool // Debugging feature to skip the allowlist of known metrics
}

// LoadOrStore returns a MetricHandle for the given distribution metric. If the metric key does not exist, it will be created.
func (d *distributions) LoadOrStore(namespace Namespace, name string, tags map[string]string) MetricHandle {
	if !knownmetrics.IsKnownMetricName(name) {
		log.Debug("telemetry: metric name %q is not a known metric, please update the list of metrics name or check that your wrote the name correctly. "+
			"The metric will still be sent.", name)
	}

	compiledTags := ""
	for k, v := range tags {
		compiledTags += k + ":" + v + ","
	}

	key := distributionKey{namespace: namespace, name: name, tags: strings.TrimSuffix(compiledTags, ",")}

	// Max size is a 2^14 array of float64 (2^3 bytes) which makes a distribution 128KB bytes array _at worse_.
	// Considering we add a point per user request on a simple http server, we would be losing data after 2^14 requests per minute or about 280 requests per second or under 3ms per request.
	// If this throughput is constant, the telemetry client flush ticker speed will increase to, at best, double twice to flush 15 seconds of data each time.
	// Which will bring our max throughput to 1100 points per second or about 750µs per request.
	// TODO: tweak this value once we get telemetry data from the telemetry client
	handle, _ := d.store.LoadOrStore(key, &distribution{key: key, values: internal.NewRingQueue[float64](1<<8, 1<<14)})

	return handle
}

func (d *distributions) Payload() transport.Payload {
	series := make([]transport.DistributionSeries, 0, d.store.Len())
	d.store.Range(func(_ distributionKey, handle *distribution) bool {
		if payload := handle.payload(); payload.Metric != "" {
			series = append(series, payload)
		}
		return true
	})

	if len(series) == 0 {
		return nil
	}

	return transport.Distributions{Series: series, SkipAllowlist: d.skipAllowlist}
}

type distribution struct {
	key distributionKey

	newSubmit atomic.Bool
	values    *internal.RingQueue[float64]
}

var logLossOnce sync.Once

func (d *distribution) Submit(value float64) {
	d.newSubmit.Store(true)
	if !d.values.Enqueue(value) {
		logLossOnce.Do(func() {
			log.Debug("telemetry: distribution %q is losing values because the buffer is full", d.key.name)
		})
	}
}

func (d *distribution) payload() transport.DistributionSeries {
	if submit := d.newSubmit.Swap(false); !submit {
		return transport.DistributionSeries{}
	}

	var tags []string
	if d.key.tags != "" {
		tags = strings.Split(d.key.tags, ",")
	}

	data := transport.DistributionSeries{
		Metric:    d.key.name,
		Namespace: d.key.namespace,
		Tags:      tags,
		Common:    knownmetrics.IsCommonMetricName(d.key.name),
		Points:    d.values.Flush(),
	}

	return data
}
