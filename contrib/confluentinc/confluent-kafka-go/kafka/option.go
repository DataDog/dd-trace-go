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

	"github.com/confluentinc/confluent-kafka-go/kafka"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

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

// Option describes options for the Kafka integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewConsumer, NewProducer, WrapConsumer and WrapProducer.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func newConfig(opts ...Option) *config {
	cfg := &config{
		ctx:           context.Background(),
		analyticsRate: instr.AnalyticsRate(false),
	}
	cfg.dataStreamsEnabled = instr.DataStreamsEnabled()

	cfg.consumerServiceName = instr.ServiceName(instrumentation.ComponentConsumer, nil)
	cfg.producerServiceName = instr.ServiceName(instrumentation.ComponentProducer, nil)
	cfg.consumerSpanName = instr.OperationName(instrumentation.ComponentConsumer, nil)
	cfg.producerSpanName = instr.OperationName(instrumentation.ComponentProducer, nil)

	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.apply(cfg)
	}
	return cfg
}

// WithService sets the config service name to serviceName.
func WithService(serviceName string) OptionFn {
	return func(cfg *config) {
		cfg.consumerServiceName = serviceName
		cfg.producerServiceName = serviceName
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
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
func WithAnalyticsRate(rate float64) OptionFn {
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
func WithCustomTag(tag string, tagFn func(msg *kafka.Message) interface{}) OptionFn {
	return func(cfg *config) {
		if cfg.tagFns == nil {
			cfg.tagFns = make(map[string]func(msg *kafka.Message) interface{})
		}
		cfg.tagFns[tag] = tagFn
	}
}

// WithConfig extracts the config information for the client to be tagged
func WithConfig(cg *kafka.ConfigMap) OptionFn {
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
func WithDataStreams() OptionFn {
	return func(cfg *config) {
		cfg.dataStreamsEnabled = true
	}
}
