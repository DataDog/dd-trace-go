// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"os"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// Start starts the tracer with the given set of options. It will stop and replace
// any running tracer, meaning that calling it several times will result in a restart
// of the tracer by replacing the current instance with a new one.
func Start(opts ...StartOption) {
	// Workaround to make sure our v1 shim behaves like the previous v1 tracer.
	if os.Getenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED") == "" {
		os.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")
	}
	// Workaround to force globalconfig to be present in the binary.
	// The condition makes no sense, but it makes sure that the compiler don't optimize it away.
	if !log.DebugEnabled() {
		log.Debug("%s", globalconfig.RuntimeID())
	}
	v2.Start(opts...)
}

// Stop stops the started tracer. Subsequent calls are valid but become no-op.
func Stop() {
	v2.Stop()
}

// Span is an alias for ddtrace.Span. It is here to allow godoc to group methods returning
// ddtrace.Span. It is recommended and is considered more correct to refer to this type as
// ddtrace.Span instead.
type Span = ddtrace.Span

// StartSpan starts a new span with the given operation name and set of options.
// If the tracer is not started, calling this function is a no-op.
func StartSpan(operationName string, opts ...StartSpanOption) Span {
	return internal.GetGlobalTracer().StartSpan(operationName, opts...)
}

// Extract extracts a SpanContext from the carrier. The carrier is expected
// to implement TextMapReader, otherwise an error is returned.
// If the tracer is not started, calling this function is a no-op.
func Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	return internal.GetGlobalTracer().Extract(carrier)
}

// Inject injects the given SpanContext into the carrier. The carrier is
// expected to implement TextMapWriter, otherwise an error is returned.
// If the tracer is not started, calling this function is a no-op.
func Inject(ctx ddtrace.SpanContext, carrier interface{}) error {
	return internal.GetGlobalTracer().Inject(ctx, carrier)
}

// SetUser associates user information to the current trace which the
// provided span belongs to. The options can be used to tune which user
// bit of information gets monitored. In case of distributed traces,
// the user id can be propagated across traces using the WithPropagation() option.
// See https://docs.datadoghq.com/security_platform/application_security/setup_and_configure/?tab=set_user#add-user-information-to-traces
func SetUser(s Span, id string, opts ...UserMonitoringOption) {
	if sp, ok := s.(internal.SpanV2Adapter); ok {
		v2.SetUser(sp.Span, id, opts...)
		return
	}
	if sp, ok := s.(mocktracer.MockspanV2Adapter); ok {
		v2.SetUser(sp.Span.Unwrap(), id, opts...)
	}
}

// Flush flushes any buffered traces. Flush is in effect only if a tracer
// is started. Users do not have to call Flush in order to ensure that
// traces reach Datadog. It is a convenience method dedicated to a specific
// use case described below.
//
// Flush is of use in Lambda environments, where starting and stopping
// the tracer on each invocation may create too much latency. In this
// scenario, a tracer may be started and stopped by the parent process
// whereas the invocation can make use of Flush to ensure any created spans
// reach the agent.
func Flush() {
	v2.Flush()
}
