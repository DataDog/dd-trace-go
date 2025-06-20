// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mongo

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type config struct {
	serviceName  string
	spanName     string
	maxQuerySize int
}

// Option describes options for the Mongo integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewMonitor.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.spanName = instr.OperationName(instrumentation.ComponentDefault, nil)
}

// WithService sets the given service name for this integration spans.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithMaxQuerySize sets the maximum query size (in bytes) before queries
// are truncated when attached as a span tag.
//
// If max is <=0, the query is never truncated.
//
// Defaults to zero.
func WithMaxQuerySize(max int) OptionFn {
	return func(cfg *config) {
		cfg.maxQuerySize = max
	}
}
