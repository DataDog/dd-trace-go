package opentracer

import opentracing "github.com/opentracing/opentracing-go"

// ServiceName will set the given service name on the started span.
func ServiceName(name string) opentracing.StartSpanOption {
	return opentracing.Tag{"service.name", name}
}

// ResourceName will start the span using the given resource name.
func ResourceName(name string) opentracing.StartSpanOption {
	return opentracing.Tag{"resource.name", name}
}

// SpanType will set the given span type on the span that is being started.
func SpanType(name string) opentracing.StartSpanOption {
	return opentracing.Tag{"span.type", name}
}
