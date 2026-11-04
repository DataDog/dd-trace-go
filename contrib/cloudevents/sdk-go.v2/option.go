// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cloudevents

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

type config struct {
	service         string
	serviceSource   string
	messagingSystem string
	destination     string
	recordSubject   bool
	customTags      map[string]any
}

// Option describes an option for the CloudEvents integration.
type Option interface {
	apply(*config)
}

// OptionFn is a functional option for the CloudEvents integration.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) { fn(cfg) }

func defaults(cfg *config) {
	cfg.service = instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.serviceSource = string(componentName)
}

// WithService sets the service name for spans created by the integration.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.service = name
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	}
}

// WithMessagingSystem sets the underlying messaging system, such as "kafka".
func WithMessagingSystem(system string) OptionFn {
	return func(cfg *config) { cfg.messagingSystem = system }
}

// WithDestinationName sets the name of the messaging destination, such as a topic or queue name.
func WithDestinationName(name string) OptionFn {
	return func(cfg *config) { cfg.destination = name }
}

// WithSubject enables recording the CloudEvent subject on spans.
func WithSubject() OptionFn {
	return func(cfg *config) { cfg.recordSubject = true }
}

// WithCustomTag adds a tag to spans created by the integration.
func WithCustomTag(key string, value any) OptionFn {
	return func(cfg *config) {
		if cfg.customTags == nil {
			cfg.customTags = make(map[string]any)
		}
		cfg.customTags[key] = value
	}
}
