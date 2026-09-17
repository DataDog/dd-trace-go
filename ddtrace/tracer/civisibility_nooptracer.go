// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/datastreams"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

var _ Tracer = (*ciVisibilityNoopTracer)(nil)

// ciVisibilityNoopTracer keeps the CI Visibility tracer process-global. It is
// a no-op for application spans until application code starts its own tracer;
// while that tracer is active, application operations are delegated to it.
// This behavior remains opt-in through the CI Visibility no-op tracer setting.
type ciVisibilityNoopTracer struct {
	Tracer

	applicationMu locking.RWMutex
	// +checklocks:applicationMu
	applicationTracer Tracer
}

type ciVisibilitySpanRoute uint8

const (
	ciVisibilitySpanRouteCI ciVisibilitySpanRoute = iota + 1
	ciVisibilitySpanRouteApplication
)

type ciVisibilitySpanRouteKey struct {
	traceID [16]byte
	spanID  uint64
}

type ciVisibilitySpanRouteRegistry struct {
	mu locking.Mutex
	// +checklocks:mu
	routes map[ciVisibilitySpanRouteKey]ciVisibilitySpanRoute
}

// ciVisibilitySpanRoutes records routing decisions while spans are alive. The
// immutable trace and span IDs avoid retaining pooled Span values, and the
// process-wide lifetime lets a replacement CI-aware wrapper finish traces that
// were started by its predecessor.
var ciVisibilitySpanRoutes = ciVisibilitySpanRouteRegistry{
	routes: make(map[ciVisibilitySpanRouteKey]ciVisibilitySpanRoute),
}

// wrapWithCiVisibilityNoopTracer creates a wrapped version of the Tracer that only accepts CiVisibility spans
func wrapWithCiVisibilityNoopTracer(tracer Tracer) *ciVisibilityNoopTracer {
	return &ciVisibilityNoopTracer{
		Tracer: tracer,
	}
}

// StartSpan implements Tracer.
func (t *ciVisibilityNoopTracer) StartSpan(operationName string, opts ...StartSpanOption) *Span {
	if opts != nil {
		cfg := NewStartSpanConfig(opts...)
		if cfg != nil && cfg.Tags != nil {
			// Let's check if the span is a CIVisibility span.
			// If yes, we create the span.
			// If not, we just behave like a noop tracer.
			if v, ok := cfg.Tags[ext.SpanType].(string); ok && isCIVisibilitySpanType(v) {
				span := t.Tracer.StartSpan(operationName, []StartSpanOption{useConfig(cfg)}...)
				registerCIVisibilitySpanRoute(span, ciVisibilitySpanRouteCI)
				return span
			}
		}
	}
	t.applicationMu.RLock()
	applicationTracer := t.applicationTracer
	if applicationTracer != nil {
		span := applicationTracer.StartSpan(operationName, opts...)
		t.applicationMu.RUnlock()
		registerCIVisibilitySpanRoute(span, ciVisibilitySpanRouteApplication)
		return span
	}
	t.applicationMu.RUnlock()
	log.Debug("CI Visibility tracer is behaving like a noop tracer, so the span will be skipped.")
	return nil
}

// SetServiceInfo implements Tracer.
func (t *ciVisibilityNoopTracer) SetServiceInfo(service, app, appType string) {
	t.applicationMu.RLock()
	defer t.applicationMu.RUnlock()
	if applicationTracer, ok := t.applicationTracer.(interface{ SetServiceInfo(string, string, string) }); ok {
		applicationTracer.SetServiceInfo(service, app, appType)
	}
}

// Extract implements Tracer.
func (t *ciVisibilityNoopTracer) Extract(carrier any) (*SpanContext, error) {
	t.applicationMu.RLock()
	defer t.applicationMu.RUnlock()
	if t.applicationTracer != nil {
		return t.applicationTracer.Extract(carrier)
	}
	return nil, nil
}

// Inject implements Tracer.
func (t *ciVisibilityNoopTracer) Inject(context *SpanContext, carrier any) error {
	t.applicationMu.RLock()
	defer t.applicationMu.RUnlock()
	if t.applicationTracer != nil {
		return t.applicationTracer.Inject(context, carrier)
	}
	return nil
}

// SetApplicationTracer installs the tracer used for application spans while
// keeping the CI Visibility tracer as the process-global owner.
func (t *ciVisibilityNoopTracer) SetApplicationTracer(applicationTracer Tracer) bool {
	if applicationTracer == nil {
		return false
	}

	t.applicationMu.Lock()
	old := t.applicationTracer
	t.applicationTracer = applicationTracer
	t.applicationMu.Unlock()

	if old != nil && old != applicationTracer {
		old.Stop()
	}
	return true
}

func (t *ciVisibilityNoopTracer) detachApplicationTracer() Tracer {
	t.applicationMu.Lock()
	applicationTracer := t.applicationTracer
	t.applicationTracer = nil
	t.applicationMu.Unlock()
	return applicationTracer
}

// TracerForFinishedChunk routes finished chunks to the tracer that created
// them. CI Visibility traces and application traces are never mixed.
func (t *ciVisibilityNoopTracer) TracerForFinishedChunk(spans []*Span) (Tracer, bool) {
	route, ok := takeCIVisibilitySpanRoute(spans)
	if !ok {
		return nil, false
	}
	if route == ciVisibilitySpanRouteCI {
		return t.Tracer, true
	}

	t.applicationMu.RLock()
	defer t.applicationMu.RUnlock()
	if t.applicationTracer == nil {
		return nil, false
	}
	return t.applicationTracer, true
}

// CIVisibilityTracer returns the CI delegate for spans that predate another
// CI-aware wrapper, such as mocktracer.
func (t *ciVisibilityNoopTracer) CIVisibilityTracer() Tracer {
	return t.Tracer
}

// Stop implements Tracer.
func (t *ciVisibilityNoopTracer) Stop() {
	applicationTracer := t.detachApplicationTracer()
	if applicationTracer != nil {
		applicationTracer.Stop()
	}

	state := civisibility.GetState()
	ciVisibilityActive := state == civisibility.StateInitializing || state == civisibility.StateInitialized
	if _, replacedByNoop := getGlobalTracer().(*NoopTracer); ciVisibilityActive && replacedByNoop {
		// Package-level Stop swaps the global tracer before invoking Stop on the
		// previous value. Restore this wrapper so stopping application tracing
		// cannot tear down CI Visibility's transport.
		setGlobalTracer(t)
		return
	}

	if !ciVisibilityActive {
		// A running CI Visibility session may replace its concrete tracer while
		// older spans are still in flight. Keep their routes until the session
		// exits; the replacement wrapper will consume them when they finish.
		clearCIVisibilitySpanRoutes()
	}
	t.Tracer.Stop()
}

func (t *ciVisibilityNoopTracer) TracerConf() TracerConf {
	return t.Tracer.TracerConf()
}

func (t *ciVisibilityNoopTracer) Flush() {
	t.Tracer.Flush()
	t.applicationMu.RLock()
	defer t.applicationMu.RUnlock()
	if t.applicationTracer != nil {
		t.applicationTracer.Flush()
	}
}

// GetDataStreamsProcessor returns the application tracer's processor, if any.
func (t *ciVisibilityNoopTracer) GetDataStreamsProcessor() *datastreams.Processor {
	t.applicationMu.RLock()
	defer t.applicationMu.RUnlock()
	if applicationTracer, ok := t.applicationTracer.(dataStreamsContainer); ok {
		return applicationTracer.GetDataStreamsProcessor()
	}
	return nil
}

func isCIVisibilitySpanType(spanType string) bool {
	return spanType == constants.SpanTypeTest ||
		spanType == constants.SpanTypeTestSuite ||
		spanType == constants.SpanTypeTestModule ||
		spanType == constants.SpanTypeTestSession
}

func ciVisibilitySpanRouteKeyFor(span *Span) (ciVisibilitySpanRouteKey, bool) {
	if span == nil {
		return ciVisibilitySpanRouteKey{}, false
	}
	context := span.Context()
	if context == nil {
		return ciVisibilitySpanRouteKey{}, false
	}
	return ciVisibilitySpanRouteKey{
		traceID: context.TraceIDBytes(),
		spanID:  context.SpanID(),
	}, true
}

func registerCIVisibilitySpanRoute(span *Span, route ciVisibilitySpanRoute) {
	key, ok := ciVisibilitySpanRouteKeyFor(span)
	if !ok {
		return
	}
	ciVisibilitySpanRoutes.mu.Lock()
	ciVisibilitySpanRoutes.routes[key] = route
	ciVisibilitySpanRoutes.mu.Unlock()
}

func takeCIVisibilitySpanRoute(spans []*Span) (ciVisibilitySpanRoute, bool) {
	ciVisibilitySpanRoutes.mu.Lock()
	defer ciVisibilitySpanRoutes.mu.Unlock()

	var route ciVisibilitySpanRoute
	for _, span := range spans {
		key, ok := ciVisibilitySpanRouteKeyFor(span)
		if !ok {
			continue
		}
		spanRoute, ok := ciVisibilitySpanRoutes.routes[key]
		if !ok {
			continue
		}
		delete(ciVisibilitySpanRoutes.routes, key)
		if route == 0 {
			route = spanRoute
		}
	}
	return route, route != 0
}

func clearCIVisibilitySpanRoutes() {
	ciVisibilitySpanRoutes.mu.Lock()
	clear(ciVisibilitySpanRoutes.routes)
	ciVisibilitySpanRoutes.mu.Unlock()
}

func useConfig(config *StartSpanConfig) StartSpanOption {
	return func(cfg *StartSpanConfig) {
		if config == nil {
			return
		}

		cfg.Parent = config.Parent
		cfg.StartTime = config.StartTime
		cfg.Tags = config.Tags
		cfg.SpanID = config.SpanID
		cfg.Context = config.Context
		cfg.SpanLinks = config.SpanLinks
	}
}
