// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package tracing

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGCPPubsub)
}

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

func defaultConfig() *config {
	return &config{
		serviceName:     instr.ServiceName(instrumentation.ComponentConsumer, nil),
		publishSpanName: instr.OperationName(instrumentation.ComponentProducer, nil),
		receiveSpanName: instr.OperationName(instrumentation.ComponentConsumer, nil),
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
