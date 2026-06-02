// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsubtrace

import (
	"os"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const envPropagationAsSpanLinks = "DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS"

type config struct {
	serviceName            string
	serviceSource          string
	publishSpanName        string
	receiveSpanName        string
	measured               bool
	propagationAsSpanLinks bool
}

// Option describes options for the Pub/Sub integration.
type Option interface {
	apply(*config)
}

func (tr *Tracer) defaultConfig() *config {
	propagationAsSpanLinks, _ := strconv.ParseBool(os.Getenv(envPropagationAsSpanLinks))
	return &config{
		serviceName:            tr.instr.ServiceName(instrumentation.ComponentConsumer, nil),
		serviceSource:          string(tr.component),
		publishSpanName:        tr.instr.OperationName(instrumentation.ComponentProducer, nil),
		receiveSpanName:        tr.instr.OperationName(instrumentation.ComponentConsumer, nil),
		measured:               false,
		propagationAsSpanLinks: propagationAsSpanLinks,
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
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	}
}

// WithMeasured sets the measured tag for traces started by WrapReceiveHandler or Publish.
func WithMeasured() OptionFn {
	return func(cfg *config) {
		cfg.measured = true
	}
}

// WithPropagationAsSpanLinks configures the receive handler to record the producer span as a span
// link rather than as a parent span. This keeps producer and consumer traces separate while
// preserving their causal relationship. The same behavior can be enabled globally via the
// DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS environment variable.
func WithPropagationAsSpanLinks() OptionFn {
	return func(cfg *config) {
		cfg.propagationAsSpanLinks = true
	}
}
