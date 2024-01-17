// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"context"
	"math"
	"net"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

const defaultServiceName = "kafka"

type config struct {
	ctx                 context.Context
	consumerServiceName string
	producerServiceName string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
	bootstrapServers    string
	groupID             string
	tagFns              map[string]func(msg *kafka.Message) interface{}
	dataStreamsEnabled  bool
}

// An Option customizes the config.
type Option func(cfg *config)

func newConfig(opts ...Option) *config {
	cfg := &config{
		ctx: context.Background(),
		// analyticsRate: globalconfig.AnalyticsRate(),
		analyticsRate: math.NaN(),
	}
	cfg.dataStreamsEnabled = internal.BoolEnv("DD_DATA_STREAMS_ENABLED", false)
	if internal.BoolEnv("DD_TRACE_KAFKA_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	}

	cfg.consumerServiceName = namingschema.ServiceName(defaultServiceName)
	cfg.producerServiceName = namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
	cfg.consumerSpanName = namingschema.OpName(namingschema.KafkaInbound)
	cfg.producerSpanName = namingschema.OpName(namingschema.KafkaOutbound)

	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// WithContext sets the config context to ctx.
// Deprecated: This is deprecated in favor of passing the context
// via the message headers
func WithContext(ctx context.Context) Option {
	return func(cfg *config) {
		cfg.ctx = ctx
	}
}

// WithServiceName sets the config service name to serviceName.
func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.consumerServiceName = serviceName
		cfg.producerServiceName = serviceName
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithCustomTag will cause the given tagFn to be evaluated after executing
// a query and attach the result to the span tagged by the key.
func WithCustomTag(tag string, tagFn func(msg *kafka.Message) interface{}) Option {
	return func(cfg *config) {
		if cfg.tagFns == nil {
			cfg.tagFns = make(map[string]func(msg *kafka.Message) interface{})
		}
		cfg.tagFns[tag] = tagFn
	}
}

// WithConfig extracts the config information for the client to be tagged
func WithConfig(cg *kafka.ConfigMap) Option {
	return func(cfg *config) {
		if groupID, err := cg.Get("group.id", ""); err == nil {
			cfg.groupID = groupID.(string)
		}
		if bs, err := cg.Get("bootstrap.servers", ""); err == nil && bs != "" {
			for _, addr := range strings.Split(bs.(string), ",") {
				host, _, err := net.SplitHostPort(addr)
				if err == nil {
					cfg.bootstrapServers = host
					return
				}
			}
		}
	}
}

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
func WithDataStreams() Option {
	return func(cfg *config) {
		cfg.dataStreamsEnabled = true
	}
}
