// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracing

import (
	"context"
	"math"
	"net"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const defaultServiceName = "kafka"

type KafkaTracer struct {
	PrevSpan            ddtrace.Span
	ctx                 context.Context
	consumerServiceName string
	producerServiceName string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
	bootstrapServers    string
	groupID             string
	tagFns              map[string]func(msg Message) interface{}
	dsmEnabled          bool
	ckgoVersion         CKGoVersion
	librdKafkaVersion   int
}

func (tr *KafkaTracer) DSMEnabled() bool {
	return tr.dsmEnabled
}

// An Option customizes the KafkaTracer.
type Option func(tr *KafkaTracer)

func NewKafkaTracer(ckgoVersion CKGoVersion, librdKafkaVersion int, opts ...Option) *KafkaTracer {
	tr := &KafkaTracer{
		ctx: context.Background(),
		// analyticsRate: globalconfig.AnalyticsRate(),
		analyticsRate:     math.NaN(),
		ckgoVersion:       ckgoVersion,
		librdKafkaVersion: librdKafkaVersion,
	}
	tr.dsmEnabled = internal.BoolEnv("DD_DATA_STREAMS_ENABLED", false)
	if internal.BoolEnv("DD_TRACE_KAFKA_ANALYTICS_ENABLED", false) {
		tr.analyticsRate = 1.0
	}

	tr.consumerServiceName = namingschema.ServiceName(defaultServiceName)
	tr.producerServiceName = namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
	tr.consumerSpanName = namingschema.OpName(namingschema.KafkaInbound)
	tr.producerSpanName = namingschema.OpName(namingschema.KafkaOutbound)

	for _, opt := range opts {
		opt(tr)
	}
	return tr
}

// WithContext sets the config context to ctx.
// Deprecated: This is deprecated in favor of passing the context
// via the message headers
func WithContext(ctx context.Context) Option {
	return func(tr *KafkaTracer) {
		tr.ctx = ctx
	}
}

// WithServiceName sets the config service name to serviceName.
func WithServiceName(serviceName string) Option {
	return func(tr *KafkaTracer) {
		tr.consumerServiceName = serviceName
		tr.producerServiceName = serviceName
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(tr *KafkaTracer) {
		if on {
			tr.analyticsRate = 1.0
		} else {
			tr.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(tr *KafkaTracer) {
		if rate >= 0.0 && rate <= 1.0 {
			tr.analyticsRate = rate
		} else {
			tr.analyticsRate = math.NaN()
		}
	}
}

// WithCustomTag will cause the given tagFn to be evaluated after executing
// a query and attach the result to the span tagged by the key.
func WithCustomTag(tag string, tagFn func(msg Message) interface{}) Option {
	return func(tr *KafkaTracer) {
		if tr.tagFns == nil {
			tr.tagFns = make(map[string]func(msg Message) interface{})
		}
		tr.tagFns[tag] = tagFn
	}
}

// WithConfig extracts the config information for the client to be tagged
func WithConfig(cg ConfigMap) Option {
	return func(tr *KafkaTracer) {
		if groupID, err := cg.Get("group.id", ""); err == nil {
			tr.groupID = groupID.(string)
		}
		if bs, err := cg.Get("bootstrap.servers", ""); err == nil && bs != "" {
			for _, addr := range strings.Split(bs.(string), ",") {
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
func WithDataStreams() Option {
	return func(tr *KafkaTracer) {
		tr.dsmEnabled = true
	}
}
