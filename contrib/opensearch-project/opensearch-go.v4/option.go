// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

package opensearch

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

type config struct {
	serviceName   string
	serviceSource string
	resourceNamer func(url, method string) string
	customTags    map[string]any
}

// Option describes an option for the OpenSearch integration.
type Option interface {
	apply(*config)
}

// OptionFn is a functional option for the OpenSearch integration.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) { fn(cfg) }

func defaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.serviceSource = string(instrumentation.PackageOpenSearchProjectOpenSearchGoV4)
	cfg.resourceNamer = quantize
}

func newConfig(opts ...Option) *config {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	return cfg
}

// WithService sets the given service name for the client.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	}
}

// WithResourceNamer specifies a quantizing function which will be used to obtain a resource name for a given
// OpenSearch request, using the request's URL and method. Note that the default quantizer obfuscates
// IDs and indexes and by replacing it, sensitive data could possibly be exposed, unless the new quantizer
// specifically takes care of that.
func WithResourceNamer(namer func(url, method string) string) OptionFn {
	return func(cfg *config) {
		cfg.resourceNamer = namer
	}
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
