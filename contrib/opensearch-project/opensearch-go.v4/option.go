// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

package opensearch

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

type config struct {
	serviceName   string
	resourceNamer func(url, method string) string
}

// Option represents an option that can be used to create or wrap a client.
type Option func(*config)

func defaultConfig() *config {
	return &config{
		serviceName:   instr.ServiceName(instrumentation.ComponentDefault, nil),
		resourceNamer: quantize,
	}
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithResourceNamer specifies a quantizing function which will be used to obtain a resource name for a given
// OpenSearch request, using the request's URL and method. Note that the default quantizer obfuscates
// IDs and indexes and by replacing it, sensitive data could possibly be exposed, unless the new quantizer
// specifically takes care of that.
func WithResourceNamer(namer func(url, method string) string) Option {
	return func(cfg *config) {
		cfg.resourceNamer = namer
	}
}
