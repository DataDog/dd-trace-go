// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpinstr"
)

// securityEvent interface allowing to lazily serialize an event into an intake
// api struct actually sending it. Additional context can be optionally added to
// the security event using the following event wrappers.
type securityEvent interface {
	toIntakeEvents() ([]*attackEvent, error)
}

type (
	// httpContext is the security event context describing an HTTP handler.
	// It includes information about its request and response.
	httpContext struct {
		Request  httpRequestContext
		Response httpResponseContext
	}

	// httpRequestContext is the HTTP request context of an HTTP operation
	// context.
	httpRequestContext struct {
		Method     string
		Host       string
		IsTLS      bool
		RequestURI string
		RemoteAddr string
		Path       string
		Headers    map[string][]string
		Query      map[string][]string
	}

	// httpResponseContext is the HTTP response context of an HTTP operation
	// context.
	httpResponseContext struct {
		Status int
	}
)

type withHTTPContext struct {
	securityEvent
	ctx httpContext
}

// withHTTPOperationContext adds the HTTP context to the event.
func withHTTPOperationContext(event securityEvent, args httpinstr.HandlerOperationArgs, res httpinstr.HandlerOperationRes) securityEvent {
	return withHTTPContext{
		securityEvent: event,
		ctx: httpContext{
			Request: httpRequestContext{
				Method:     args.Method,
				Host:       args.Host,
				IsTLS:      args.IsTLS,
				RequestURI: args.RequestURI,
				Path:       args.Path,
				RemoteAddr: args.RemoteAddr,
				Headers:    args.Headers,
				Query:      args.Query,
			},
			Response: httpResponseContext{
				Status: res.Status,
			},
		},
	}
}

// toIntakeEvent converts the current event with the HTTP context into an
// intake security event.
func (e withHTTPContext) toIntakeEvents() ([]*attackEvent, error) {
	events, err := e.securityEvent.toIntakeEvents()
	if err != nil {
		return nil, err
	}
	reqContext := makeAttackContextHTTPRequest(e.ctx.Request)
	resContext := makeAttackContextHTTPResponse(e.ctx.Response.Status)
	httpContext := makeAttackContextHTTP(reqContext, resContext)
	for _, event := range events {
		event.Context.HTTP = httpContext
	}
	return events, nil
}

// makeAttackContextHTTPRequest create the api.attackContextHTTPRequest payload
// from the given httpRequestContext.
func makeAttackContextHTTPRequest(req httpRequestContext) attackContextHTTPRequest {
	host, portStr := splitHostPort(req.Host)
	port, _ := strconv.Atoi(portStr)
	remoteIP, remotePortStr := splitHostPort(req.RemoteAddr)
	remotePort, _ := strconv.Atoi(remotePortStr)
	var scheme string
	if req.IsTLS {
		scheme = "https"
	} else {
		scheme = "http"
	}
	url := makeHTTPURL(scheme, req.Host, req.Path)
	headers := makeHTTPHeaders(req.Headers)
	return attackContextHTTPRequest{
		Scheme:     scheme,
		Method:     req.Method,
		URL:        url,
		Host:       host,
		Port:       port,
		Path:       req.Path,
		RemoteIP:   remoteIP,
		RemotePort: remotePort,
		Headers:    headers,
		Parameters: attackContextHTTPRequestParameters{Query: req.Query},
	}
}

type spanContext struct {
	securityEvent
	traceID, spanID uint64
}

// withSpanContext adds the span context to the event.
func withSpanContext(event securityEvent, traceID, spanID uint64) securityEvent {
	return spanContext{
		securityEvent: event,
		traceID:       traceID,
		spanID:        spanID,
	}
}

// ToIntakeEvent converts the current event with the span context into an
// intake security event.
func (ctx spanContext) toIntakeEvents() ([]*attackEvent, error) {
	events, err := ctx.securityEvent.toIntakeEvents()
	if err != nil {
		return nil, err
	}
	traceID := strconv.FormatUint(ctx.traceID, 10)
	spanID := strconv.FormatUint(ctx.spanID, 10)
	traceContext := makeAttackContextTrace(traceID)
	spanContext := MakeAttackContextSpan(spanID)
	for _, event := range events {
		event.Context.Trace = traceContext
		event.Context.Span = spanContext
	}
	return events, nil
}

type serviceContext struct {
	securityEvent
	name, version, environment string
}

// withServiceContext adds the service context to the event.
func withServiceContext(event securityEvent, name, version, environment string) securityEvent {
	return serviceContext{
		securityEvent: event,
		name:          name,
		version:       version,
		environment:   environment,
	}
}

// ToIntakeEvent converts the current event with the service context into an
// intake security event.
func (ctx serviceContext) toIntakeEvents() ([]*attackEvent, error) {
	events, err := ctx.securityEvent.toIntakeEvents()
	if err != nil {
		return nil, err
	}
	serviceContext := makeServiceContext(ctx.name, ctx.version, ctx.environment)
	for _, event := range events {
		event.Context.Service = serviceContext
	}
	return events, nil
}

type tagsContext struct {
	securityEvent
	tags []string
}

// withTagsContext adds the tags context to the event.
func withTagsContext(event securityEvent, tags []string) securityEvent {
	return tagsContext{
		securityEvent: event,
		tags:          tags,
	}
}

// ToIntakeEvent converts the current event with the tags context into an
// intake security event.
func (ctx tagsContext) toIntakeEvents() ([]*attackEvent, error) {
	events, err := ctx.securityEvent.toIntakeEvents()
	if err != nil {
		return nil, err
	}
	tagsContext := newAttackContextTags(ctx.tags)
	for _, event := range events {
		event.Context.Tags = tagsContext
	}
	return events, nil
}

type tracerContext struct {
	securityEvent
	runtime, runtimeVersion, version string
}

// withTracerContext adds the tracer context to the event.
func withTracerContext(event securityEvent, runtime, runtimeVersion, version string) securityEvent {
	return tracerContext{
		securityEvent:  event,
		runtime:        runtime,
		runtimeVersion: runtimeVersion,
		version:        version,
	}
}

// ToIntakeEvent converts the current event with the tracer context into an
// intake security event.
func (ctx tracerContext) toIntakeEvents() ([]*attackEvent, error) {
	events, err := ctx.securityEvent.toIntakeEvents()
	if err != nil {
		return nil, err
	}
	tracerContext := makeAttackContextTracer(ctx.version, ctx.runtime, ctx.runtimeVersion)
	for _, event := range events {
		event.Context.Tracer = tracerContext
	}
	return events, nil
}

// withHostContext adds the running host context to the event.
func withHostContext(event securityEvent, hostname, osname string) securityEvent {
	return hostContext{
		securityEvent: event,
		hostname:      hostname,
		osname:        osname,
	}
}

type hostContext struct {
	securityEvent
	hostname, osname string
}

// ToIntakeEvent converts the current event with the host context into an intake
// security event.
func (ctx hostContext) toIntakeEvents() ([]*attackEvent, error) {
	events, err := ctx.securityEvent.toIntakeEvents()
	if err != nil {
		return nil, err
	}
	hostContext := makeAttackContextHost(ctx.hostname, ctx.osname)
	for _, event := range events {
		event.Context.Host = hostContext
	}
	return events, nil
}
