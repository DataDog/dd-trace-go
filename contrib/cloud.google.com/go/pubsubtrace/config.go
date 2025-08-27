// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsubtrace

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

type config struct {
	serviceName     string
	publishSpanName string
	receiveSpanName string
	measured        bool
}

// Option describes options for the Pub/Sub integration.
type Option interface {
	apply(*config)
}

func (tr *Tracer) defaultConfig() *config {
	return &config{
		serviceName:     tr.instr.ServiceName(instrumentation.ComponentConsumer, nil),
		publishSpanName: tr.instr.OperationName(instrumentation.ComponentProducer, nil),
		receiveSpanName: tr.instr.OperationName(instrumentation.ComponentConsumer, nil),
		measured:        false,
	}
}

// OptionFn represents options applicable to WrapReceiveHandler or Publish.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

// WithService sets the service name tag for traces started by WrapReceiveHandler or Publish.
func WithService(serviceName string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}

// WithMeasured sets the measured tag for traces started by WrapReceiveHandler or Publish.
func WithMeasured() OptionFn {
	return func(cfg *config) {
		cfg.measured = true
	}
}
