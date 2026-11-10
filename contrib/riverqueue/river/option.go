// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package river

import (
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// Option describes an option for the River integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewMiddleware.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

type config struct {
	// service is set by WithService, or lazily by resolveService.
	service            atomic.Pointer[resolvedService]
	distributedTracing bool
	errCheck           func(err error) bool
	tags               map[string]any
}

func defaults(cfg *config) {
	cfg.distributedTracing = true
}

func newConfig(opts ...Option) *config {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		if opt != nil {
			opt.apply(cfg)
		}
	}
	return cfg
}

// resolvedService is an immutable service name paired with its prebuilt start option.
type resolvedService struct {
	name string
	opt  tracer.StartSpanOption
}

func newResolvedService(name, source string) *resolvedService {
	return &resolvedService{
		name: name,
		opt:  tracer.Tag(ext.KeyServiceSource, instrumentation.ServiceOverride{Name: name, Source: source}),
	}
}

// resolveService returns the WithService value or the default (DD_SERVICE). The
// default is only cached once the tracer is initialized, because under
// Orchestrion a River client can be built before tracer.Start runs.
func (cfg *config) resolveService() *resolvedService {
	if svc := cfg.service.Load(); svc != nil {
		return svc
	}
	name, source := instr.ServiceName(instrumentation.ComponentDefault, nil), string(instrumentation.PackageRiverqueueRiver)
	if name == "" {
		// The tracer's own default service is not an override.
		source = ""
	}
	svc := newResolvedService(name, source)
	if instr.TracerInitialized() {
		cfg.service.Store(svc)
	}
	return svc
}

// WithService sets the service name of the spans created by the middleware.
// It defaults to the global service name (DD_SERVICE).
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.service.Store(newResolvedService(name, instrumentation.ServiceSourceWithServiceOption))
	}
}

// WithDistributedTracing enables or disables trace context propagation through
// job metadata. When enabled (the default), the insert span's context, including
// its sampling decision, is stored under the "_datadog" key of each inserted
// job's metadata, and the span that works the job continues that trace.
func WithDistributedTracing(enabled bool) OptionFn {
	return func(cfg *config) {
		cfg.distributedTracing = enabled
	}
}

// WithErrorCheck sets a function that decides whether an error returned by a
// worker or an insert marks the span as an error. Returning false for an error
// leaves the span without error tags. Snoozed jobs are never errors, regardless
// of this function.
func WithErrorCheck(fn func(err error) bool) OptionFn {
	return func(cfg *config) {
		cfg.errCheck = fn
	}
}

// WithCustomTag sets a custom tag on every span created by the middleware.
func WithCustomTag(key string, value any) OptionFn {
	return func(cfg *config) {
		if cfg.tags == nil {
			cfg.tags = make(map[string]any)
		}
		cfg.tags[key] = value
	}
}
