package opentelemetry

import (
	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var locOpts []tracer.StartOption

// WithOpts is used to pass Datadog options to Otel interface.
// Might be reconfigured or removed later. Kept for development purposes.
func WithOpts(opts ...tracer.StartOption) (_ oteltrace.TracerOption) {
	locOpts = opts
	return
}
