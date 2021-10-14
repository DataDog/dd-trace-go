// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package types

import (
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/intake/api"
)

// SecurityEvent is a generic security event payload holding an actual security event (eg. a WAF security event),
// along with its optional context.
type SecurityEvent interface {
	ToIntakeEvent() ([]*api.AttackEvent, error)
}

type (
	// HTTPContext is the security event context describing an HTTP handler.
	// It includes information about its request and response.
	HTTPContext struct {
		Request  HTTPRequestContext
		Response HTTPResponseContext
	}

	// HTTPRequestContext is the HTTP request context of an HTTP operation
	// context.
	HTTPRequestContext struct {
		Method     string
		Host       string
		IsTLS      bool
		RequestURI string
		RemoteAddr string
		Path       string
		Headers    map[string][]string
		Query      map[string][]string
	}

	// HTTPResponseContext is the HTTP response context of an HTTP operation
	// context.
	HTTPResponseContext struct {
		Status int
	}
)

type withHTTPContext struct {
	SecurityEvent
	ctx HTTPContext
}

// WithHTTPContext adds the HTTP context to the event.
func WithHTTPContext(event SecurityEvent, ctx HTTPContext) SecurityEvent {
	return withHTTPContext{
		SecurityEvent: event,
		ctx:           ctx,
	}
}

// ToIntakeEvent converts the current event with the HTTP context into an
// intake security event.
func (e withHTTPContext) ToIntakeEvent() ([]*api.AttackEvent, error) {
	events, err := e.SecurityEvent.ToIntakeEvent()
	if err != nil {
		return nil, err
	}
	reqContext := makeAttackContextHTTPRequest(e.ctx.Request)
	resContext := api.MakeAttackContextHTTPResponse(e.ctx.Response.Status)
	httpContext := api.MakeAttackContextHTTP(reqContext, resContext)
	for _, event := range events {
		event.Context.HTTP = httpContext
	}
	return events, nil
}

// makeAttackContextHTTPRequest create the api.AttackContextHTTPRequest payload
// from the given HTTPRequestContext.
func makeAttackContextHTTPRequest(req HTTPRequestContext) api.AttackContextHTTPRequest {
	host, portStr := api.SplitHostPort(req.Host)
	port, _ := strconv.Atoi(portStr)
	remoteIP, remotePortStr := api.SplitHostPort(req.RemoteAddr)
	remotePort, _ := strconv.Atoi(remotePortStr)
	var scheme string
	if req.IsTLS {
		scheme = "https"
	} else {
		scheme = "http"
	}
	url := api.MakeHTTPURL(scheme, req.Host, req.Path)
	headers := api.MakeHTTPHeaders(req.Headers)
	return api.AttackContextHTTPRequest{
		Scheme:     scheme,
		Method:     req.Method,
		URL:        url,
		Host:       host,
		Port:       port,
		Path:       req.Path,
		RemoteIP:   remoteIP,
		RemotePort: remotePort,
		Headers:    headers,
		Parameters: api.AttackContextHTTPRequestParameters{Query: req.Query},
	}
}

type withSpanContext struct {
	SecurityEvent
	traceID, spanID uint64
}

// WithSpanContext adds the span context to the event.
func WithSpanContext(event SecurityEvent, traceID, spanID uint64) SecurityEvent {
	return withSpanContext{
		SecurityEvent: event,
		traceID:       traceID,
		spanID:        spanID,
	}
}

// ToIntakeEvent converts the current event with the span context into an
// intake security event.
func (ctx withSpanContext) ToIntakeEvent() ([]*api.AttackEvent, error) {
	events, err := ctx.SecurityEvent.ToIntakeEvent()
	if err != nil {
		return nil, err
	}
	traceID := strconv.FormatUint(ctx.traceID, 10)
	spanID := strconv.FormatUint(ctx.spanID, 10)
	traceContext := api.MakeAttackContextTrace(traceID)
	spanContext := api.MakeAttackContextSpan(spanID)
	for _, event := range events {
		event.Context.Trace = traceContext
		event.Context.Span = spanContext
	}
	return events, nil
}

type withServiceContext struct {
	SecurityEvent
	name, version, environment string
}

// WithServiceContext adds the service context to the event.
func WithServiceContext(event SecurityEvent, name, version, environment string) SecurityEvent {
	return withServiceContext{
		SecurityEvent: event,
		name:          name,
		version:       version,
		environment:   environment,
	}
}

// ToIntakeEvent converts the current event with the service context into an
// intake security event.
func (ctx withServiceContext) ToIntakeEvent() ([]*api.AttackEvent, error) {
	events, err := ctx.SecurityEvent.ToIntakeEvent()
	if err != nil {
		return nil, err
	}
	serviceContext := api.MakeServiceContext(ctx.name, ctx.version, ctx.environment)
	for _, event := range events {
		event.Context.Service = serviceContext
	}
	return events, nil
}

type withTagsContext struct {
	SecurityEvent
	tags []string
}

// WithTagsContext adds the tags context to the event.
func WithTagsContext(event SecurityEvent, tags []string) SecurityEvent {
	return withTagsContext{
		SecurityEvent: event,
		tags:          tags,
	}
}

// ToIntakeEvent converts the current event with the tags context into an
// intake security event.
func (ctx withTagsContext) ToIntakeEvent() ([]*api.AttackEvent, error) {
	events, err := ctx.SecurityEvent.ToIntakeEvent()
	if err != nil {
		return nil, err
	}
	tagsContext := api.NewAttackContextTags(ctx.tags)
	for _, event := range events {
		event.Context.Tags = tagsContext
	}
	return events, nil
}

type withTracerContext struct {
	SecurityEvent
	runtime, runtimeVersion, version string
}

// WithTracerContext adds the tracer context to the event.
func WithTracerContext(event SecurityEvent, runtime, runtimeVersion, version string) SecurityEvent {
	return withTracerContext{
		SecurityEvent:  event,
		runtime:        runtime,
		runtimeVersion: runtimeVersion,
		version:        version,
	}
}

// ToIntakeEvent converts the current event with the tracer context into an
// intake security event.
func (ctx withTracerContext) ToIntakeEvent() ([]*api.AttackEvent, error) {
	events, err := ctx.SecurityEvent.ToIntakeEvent()
	if err != nil {
		return nil, err
	}
	tracerContext := api.MakeAttackContextTracer(ctx.version, ctx.runtime, ctx.runtimeVersion)
	for _, event := range events {
		event.Context.Tracer = tracerContext
	}
	return events, nil
}

// WithHostContext adds the running host context to the event.
func WithHostContext(event SecurityEvent, hostname, osname string) SecurityEvent {
	return withHostContext{
		SecurityEvent: event,
		hostname:      hostname,
		osname:        osname,
	}
}

type withHostContext struct {
	SecurityEvent
	hostname, osname string
}

// ToIntakeEvent converts the current event with the host context into an intake
// security event.
func (ctx withHostContext) ToIntakeEvent() ([]*api.AttackEvent, error) {
	events, err := ctx.SecurityEvent.ToIntakeEvent()
	if err != nil {
		return nil, err
	}
	hostContext := api.MakeAttackContextHost(ctx.hostname, ctx.osname)
	for _, event := range events {
		event.Context.Host = hostContext
	}
	return events, nil
}
