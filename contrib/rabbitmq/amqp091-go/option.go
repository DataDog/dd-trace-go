// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package amqp091

import (
	"maps"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type config struct {
	serviceName        string
	serviceSource      string
	dataStreamsEnabled bool
	customTags         map[string]any
	producerBase       *tracer.StartSpanConfig
	consumerBase       *tracer.StartSpanConfig
}

type Option interface {
	apply(*config)
}

type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) { fn(cfg) }

func defaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.serviceSource = string(componentName)
	cfg.dataStreamsEnabled = instr.DataStreamsEnabled()
}

func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	}
}

func WithCustomTag(key string, value any) OptionFn {
	return func(cfg *config) {
		if cfg.customTags == nil {
			cfg.customTags = make(map[string]any)
		}
		cfg.customTags[key] = value
	}
}

func WithDataStreams() OptionFn {
	return func(cfg *config) { cfg.dataStreamsEnabled = true }
}

var defaultConfig = sync.OnceValue(func() *config { return buildConfig(nil) })

func newConfig(opts []Option) *config {
	if len(opts) == 0 {
		return defaultConfig()
	}
	return buildConfig(opts)
}

func buildConfig(opts []Option) *config {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	cfg.producerBase = cfg.baseSpanConfig(ext.SpanKindProducer, ext.SpanTypeMessageProducer)
	cfg.consumerBase = cfg.baseSpanConfig(ext.SpanKindConsumer, ext.SpanTypeMessageConsumer)
	return cfg
}

func (cfg *config) baseSpanConfig(kind, spanType string) *tracer.StartSpanConfig {
	tags := map[string]any{
		ext.SpanKind:        kind,
		ext.SpanType:        spanType,
		ext.Component:       string(componentName),
		ext.MessagingSystem: messagingSystem,
	}
	maps.Copy(tags, cfg.customTags)
	opts := []tracer.StartSpanOption{tracer.WithTags(tags)}
	if cfg.serviceName != "" {
		opts = append(opts, instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource))
	}
	return tracer.NewStartSpanConfig(opts...)
}
