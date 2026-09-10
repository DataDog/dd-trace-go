// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package kgo

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

type config struct {
	consumerServiceName string
	producerServiceName string
	serviceSource       string
	consumerSpanName    string
	producerSpanName    string
	dataStreamsEnabled  bool
}

func defaults(cfg *config) {
	cfg.consumerServiceName = instr.ServiceName(instrumentation.ComponentConsumer, nil)
	cfg.producerServiceName = instr.ServiceName(instrumentation.ComponentProducer, nil)
	cfg.serviceSource = string(instrumentation.PackageTwmbFranzGo)
	cfg.consumerSpanName = instr.OperationName(instrumentation.ComponentConsumer, nil)
	cfg.producerSpanName = instr.OperationName(instrumentation.ComponentProducer, nil)
	cfg.dataStreamsEnabled = instr.DataStreamsEnabled()
}

// Option configures distributed tracing for the franz-go integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewClient.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) { fn(cfg) }

// WithService sets the service name for both producer and consumer spans.
func WithService(serviceName string) Option {
	return OptionFn(func(cfg *config) {
		cfg.consumerServiceName = serviceName
		cfg.producerServiceName = serviceName
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	})
}

// WithDataStreams enables the Data Streams Monitoring product features:
// https://www.datadoghq.com/product/data-streams-monitoring/
func WithDataStreams() Option {
	return OptionFn(func(cfg *config) {
		cfg.dataStreamsEnabled = true
	})
}
