// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracing

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageSegmentioKafkaGo)
}

const dsmEdgeTagCacheMax = 1000

// dsmEdgeTagCache caches edge-tag slices keyed by a composite string to avoid
// per-message []string allocations. Entries are shared read-only after store;
// callers must not mutate the returned slice.
type dsmEdgeTagCache struct {
	m    sync.Map
	size atomic.Int32
}

func (c *dsmEdgeTagCache) get(key string) []string {
	if v, ok := c.m.Load(key); ok {
		return v.([]string)
	}
	return nil
}

func (c *dsmEdgeTagCache) getOrStore(key string, tags []string) []string {
	if v, ok := c.m.Load(key); ok {
		return v.([]string)
	}
	if c.size.Load() >= dsmEdgeTagCacheMax {
		return tags
	}
	actual, loaded := c.m.LoadOrStore(key, tags)
	if !loaded {
		c.size.Add(1)
	}
	return actual.([]string)
}

type Tracer struct {
	consumerServiceName string
	producerServiceName string
	serviceSource       string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
	dataStreamsEnabled  bool
	kafkaCfg            KafkaConfig
	clusterID           atomic.Value // +checkatomic
	dsmTagCache         dsmEdgeTagCache
}

// Option describes options for the Kafka integration.
type Option interface {
	apply(*Tracer)
}

func NewTracer(kafkaCfg KafkaConfig, opts ...Option) *Tracer {
	tr := &Tracer{
		consumerServiceName: instr.ServiceName(instrumentation.ComponentConsumer, nil),
		producerServiceName: instr.ServiceName(instrumentation.ComponentProducer, nil),
		serviceSource:       string(instrumentation.PackageSegmentioKafkaGo),
		consumerSpanName:    instr.OperationName(instrumentation.ComponentConsumer, nil),
		producerSpanName:    instr.OperationName(instrumentation.ComponentProducer, nil),
		analyticsRate:       instr.AnalyticsRate(false),
		dataStreamsEnabled:  instr.DataStreamsEnabled(),
		kafkaCfg:            kafkaCfg,
	}
	for _, opt := range opts {
		opt.apply(tr)
	}
	return tr
}

// OptionFn represents options applicable to NewReader, NewWriter, WrapReader and WrapWriter.
type OptionFn func(*Tracer)

func (fn OptionFn) apply(cfg *Tracer) {
	fn(cfg)
}

// WithService sets the Tracer service name to serviceName.
func WithService(serviceName string) Option {
	return OptionFn(func(tr *Tracer) {
		tr.consumerServiceName = serviceName
		tr.producerServiceName = serviceName
		tr.serviceSource = instrumentation.ServiceSourceWithServiceOption
	})
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return OptionFn(func(tr *Tracer) {
		if on {
			tr.analyticsRate = 1.0
		} else {
			tr.analyticsRate = math.NaN()
		}
	})
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return OptionFn(func(tr *Tracer) {
		if rate >= 0.0 && rate <= 1.0 {
			tr.analyticsRate = rate
		} else {
			tr.analyticsRate = math.NaN()
		}
	})
}

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
func WithDataStreams() Option {
	return OptionFn(func(tr *Tracer) {
		tr.dataStreamsEnabled = true
	})
}

func (tr *Tracer) DSMEnabled() bool {
	return tr.dataStreamsEnabled
}

func (tr *Tracer) ClusterID() string {
	v, _ := tr.clusterID.Load().(string)
	return v
}

func (tr *Tracer) SetClusterID(id string) {
	tr.clusterID.Store(id)
}

func Logger() instrumentation.Logger {
	return instr.Logger()
}
