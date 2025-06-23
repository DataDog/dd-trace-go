// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"net/http"
	"path"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"

	envoycore "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

// spanManager handles the lifecycle of trace spans for Envoy requests
type spanManager struct {
	componentName string
}

// newSpanManager creates a new spanManager
func newSpanManager(isGCPServiceExtension bool) *spanManager {
	componentName := componentNameEnvoy
	if isGCPServiceExtension {
		componentName = componentNameGCPServiceExtension
	}

	return &spanManager{
		componentName: componentName,
	}
}

// StartSpan begins a new trace span for the incoming request
func (sm *spanManager) StartSpan(request *http.Request, writer http.ResponseWriter) (
	span *tracer.Span,
	wrappedWriter http.ResponseWriter,
	spanRequest *http.Request,
	afterHandle func(),
	blocked bool,
) {
	wrappedWriter, spanRequest, afterHandle, blocked = httptrace.BeforeHandle(&httptrace.ServeConfig{
		Framework: "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3",
		Resource:  request.Method + " " + path.Clean(request.URL.Path),
		SpanOpts: []tracer.StartSpanOption{
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			tracer.Tag(ext.Component, sm.componentName),
		},
	}, writer, request)

	span, ok := tracer.SpanFromContext(spanRequest.Context())
	if !ok {
		span = nil
	}
	return span, wrappedWriter, spanRequest, afterHandle, blocked
}

// InjectPropagationHeaders creates header mutations for trace propagation
func (sm *spanManager) InjectPropagationHeaders(span *tracer.Span) ([]*envoycore.HeaderValueOption, error) {
	newHeaders := make(http.Header)
	if err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(newHeaders)); err != nil {
		return nil, err
	}

	if len(newHeaders) > 0 {
		instr.Logger().Debug("external_processing: injecting propagation headers: %v\n", newHeaders)
	}

	return convertHeadersToEnvoy(newHeaders), nil
}
