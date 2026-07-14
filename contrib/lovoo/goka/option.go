// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package goka

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

// defaultLoopSuffix mirrors goka's own default loop-topic suffix. goka exposes
// no accessor for the current suffix, so we track it here and let callers who
// change it via goka.SetLoopSuffix mirror the change with WithLoopSuffix.
const defaultLoopSuffix = "-loop"

type config struct {
	consumerServiceName string
	serviceSource       string
	consumerSpanName    string
	dataStreamsEnabled  bool
	loopSuffix          string
}

func defaults(cfg *config) {
	cfg.consumerServiceName = instr.ServiceName(instrumentation.ComponentConsumer, nil)
	cfg.serviceSource = string(instrumentation.PackageLovooGoka)
	cfg.consumerSpanName = instr.OperationName(instrumentation.ComponentConsumer, nil)
	cfg.dataStreamsEnabled = instr.DataStreamsEnabled()
	cfg.loopSuffix = defaultLoopSuffix
}

// Option configures the goka integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewTracer.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) { fn(cfg) }

// WithService sets the service name for the kafka.consume spans.
func WithService(serviceName string) Option {
	return OptionFn(func(cfg *config) {
		cfg.consumerServiceName = serviceName
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

// WithLoopSuffix sets the loop-topic suffix used to tag Loopback spans and DSM
// checkpoints. It must match the suffix configured in goka via goka.SetLoopSuffix
// (default "-loop"); set it only if the application changed goka's suffix.
func WithLoopSuffix(suffix string) Option {
	return OptionFn(func(cfg *config) {
		cfg.loopSuffix = suffix
	})
}
