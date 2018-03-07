package opentracer // import "gopkg.in/DataDog/dd-trace-go.v0/ddtrace/opentracer"

import (
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/ext"

	opentracing "github.com/opentracing/opentracing-go"
)

// ServiceName will set the given service name on the started span.
func ServiceName(name string) opentracing.StartSpanOption {
	return opentracing.Tag{Key: ext.ServiceName, Value: name}
}

// ResourceName will start the span using the given resource name.
func ResourceName(name string) opentracing.StartSpanOption {
	return opentracing.Tag{Key: ext.ResourceName, Value: name}
}

// SpanType will set the given span type on the span that is being started.
func SpanType(name string) opentracing.StartSpanOption {
	return opentracing.Tag{Key: ext.SpanType, Value: name}
}
