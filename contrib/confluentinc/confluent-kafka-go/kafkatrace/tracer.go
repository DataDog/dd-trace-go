// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"context"
	"math"
	"net"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/kafkaclusterid"
)

type Tracer struct {
	PrevSpan            *tracer.Span
	ClusterIDFetcher    kafkaclusterid.Fetcher
	ctx                 context.Context
	consumerServiceName string
	producerServiceName string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
	bootstrapServers    string
	groupID             string
	tagFns              map[string]func(msg Message) any
	dsmEnabled          bool
	ckgoVersion         CKGoVersion
	librdKafkaVersion   int
}

func (tr *Tracer) DSMEnabled() bool {
	return tr.dsmEnabled
}

func (tr *Tracer) ClusterID() string {
	return tr.ClusterIDFetcher.ID()
}

func (tr *Tracer) SetClusterID(id string) {
	tr.ClusterIDFetcher.SetID(id)
}

// FetchClusterIDAsync launches a background goroutine to fetch the cluster ID.
// The goroutine is cancelled when StopClusterIDFetch is called.
func (tr *Tracer) FetchClusterIDAsync(fetchFn func(ctx context.Context) string) {
	tr.ClusterIDFetcher.FetchAsync(fetchFn)
}

// StopClusterIDFetch cancels any in-flight cluster ID fetch and waits for the
// goroutine to finish cleanup. This returns near-instantly because the context
// cancellation causes in-flight network calls to abort.
func (tr *Tracer) StopClusterIDFetch() {
	tr.ClusterIDFetcher.Stop()
}

// WaitForClusterID blocks until any in-flight cluster ID fetch completes.
// Use this in tests to ensure the cluster ID is available before asserting.
func (tr *Tracer) WaitForClusterID() {
	tr.ClusterIDFetcher.Wait()
}

type Option interface {
	apply(*Tracer)
}

// OptionFn represents options applicable to NewConsumer, NewProducer, WrapConsumer and WrapProducer.
type OptionFn func(*Tracer)

func (fn OptionFn) apply(cfg *Tracer) {
	fn(cfg)
}

func NewKafkaTracer(instr *instrumentation.Instrumentation, ckgoVersion CKGoVersion, librdKafkaVersion int, opts ...Option) *Tracer {
	tr := &Tracer{
		ctx:               context.Background(),
		analyticsRate:     instr.AnalyticsRate(false),
		ckgoVersion:       ckgoVersion,
		librdKafkaVersion: librdKafkaVersion,
	}
	if internal.BoolEnv("DD_TRACE_KAFKA_ANALYTICS_ENABLED", false) {
		tr.analyticsRate = 1.0
	}

	tr.dsmEnabled = instr.DataStreamsEnabled()

	tr.consumerServiceName = instr.ServiceName(instrumentation.ComponentConsumer, nil)
	tr.producerServiceName = instr.ServiceName(instrumentation.ComponentProducer, nil)
	tr.consumerSpanName = instr.OperationName(instrumentation.ComponentConsumer, nil)
	tr.producerSpanName = instr.OperationName(instrumentation.ComponentProducer, nil)

	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.apply(tr)
	}
	return tr
}

// WithContext sets the config context to ctx.
// Deprecated: This is deprecated in favor of passing the context
// via the message headers
func WithContext(ctx context.Context) OptionFn {
	return func(tr *Tracer) {
		tr.ctx = ctx
	}
}

// WithService sets the config service name to serviceName.
func WithService(serviceName string) OptionFn {
	return func(cfg *Tracer) {
		cfg.consumerServiceName = serviceName
		cfg.producerServiceName = serviceName
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	return func(cfg *Tracer) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *Tracer) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithCustomTag will cause the given tagFn to be evaluated after executing
// a query and attach the result to the span tagged by the key.
func WithCustomTag(tag string, tagFn func(msg Message) any) OptionFn {
	return func(cfg *Tracer) {
		if cfg.tagFns == nil {
			cfg.tagFns = make(map[string]func(msg Message) any)
		}
		cfg.tagFns[tag] = tagFn
	}
}

// WithConfig extracts the config information for the client to be tagged
func WithConfig(cg ConfigMap) OptionFn {
	return func(tr *Tracer) {
		if groupID, err := cg.Get("group.id", ""); err == nil {
			tr.groupID = groupID.(string)
		}
		if bs, err := cg.Get("bootstrap.servers", ""); err == nil && bs != "" {
			for addr := range strings.SplitSeq(bs.(string), ",") {
				host, _, err := net.SplitHostPort(addr)
				if err == nil {
					tr.bootstrapServers = host
					return
				}
			}
		}
	}
}

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
func WithDataStreams() OptionFn {
	return func(tr *Tracer) {
		tr.dsmEnabled = true
	}
}
