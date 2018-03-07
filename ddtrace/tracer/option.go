package tracer

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/ext"
)

// config holds the tracer configuration.
type config struct {
	// debug, when true, writes details to logs.
	debug bool

	// serviceName specifies the name of this application.
	serviceName string

	// sampler specifies the sampler that will be used for sampling traces.
	sampler Sampler

	// agentAddr specifies the hostname and  of the agent where the traces
	// are sent to.
	agentAddr string

	// globalTags holds a set of tags that will be automatically applied to
	// all spans.
	globalTags map[string]interface{}

	// transport specifies the Transport interface which will be used to send data to the agent.
	transport transport

	// textMapPropagator propagates text maps
	textMapPropagator Propagator
}

// StartOption represents a function that can be provided as a parameter to Start.
type StartOption func(*config)

// defaults sets the default values for a config.
func defaults(c *config) {
	c.serviceName = filepath.Base(os.Args[0])
	c.sampler = NewAllSampler()
	c.agentAddr = defaultAddress
}

// WithDebugMode enables debug mode on the tracer, making logging more verbose.
func WithDebugMode(enabled bool) StartOption {
	return func(c *config) {
		c.debug = enabled
	}
}

// WithTextMapPropagator sets a custom TextMap propagator on the tracer.
func WithTextMapPropagator(p Propagator) StartOption {
	return func(c *config) {
		c.textMapPropagator = p
	}
}

// WithServiceName sets the default service name to be used with the tracer.
func WithServiceName(name string) StartOption {
	return func(c *config) {
		c.serviceName = name
	}
}

// WithAgentAddr sets the address where the agent is located. The default is
// localhost:8126.
func WithAgentAddr(addr string) StartOption {
	return func(c *config) {
		c.agentAddr = addr
	}
}

// WithGlobalTag sets a key/value pair which will be set as a tag on all spans
// created by tracer.
func WithGlobalTag(k string, v interface{}) StartOption {
	return func(c *config) {
		if c.globalTags == nil {
			c.globalTags = make(map[string]interface{})
		}
		c.globalTags[k] = v
	}
}

// WithSampler sets the given sampler to be used with the tracer. By default
// an all-permissive sampler is used.
func WithSampler(s Sampler) StartOption {
	return func(c *config) {
		c.sampler = s
	}
}

// StartSpanOption is a configuration option for StartSpan.
type StartSpanOption = ddtrace.StartSpanOption

// Tag sets the given key/value pair as a tag on the started Span.
func Tag(k string, v interface{}) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		if cfg.Tags == nil {
			cfg.Tags = map[string]interface{}{}
		}
		cfg.Tags[k] = v
	}
}

// ServiceName sets the given service name on the started span.
func ServiceName(name string) StartSpanOption {
	return Tag(ext.ServiceName, name)
}

// ResourceName sets the given resource name on the started span.
func ResourceName(name string) StartSpanOption {
	return Tag(ext.ResourceName, name)
}

// SpanType sets the given span type on the started span.
func SpanType(name string) StartSpanOption {
	return Tag(ext.SpanType, name)
}

// ChildOf tells StartSpan to use the given context as a parent for the
// created span.
func ChildOf(ctx ddtrace.SpanContext) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.Parent = ctx
	}
}

// StartTime sets a custom time as the start time for the created span. By
// default a span is started using the current time.
func StartTime(t time.Time) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.StartTime = t
	}
}

// FinishOption is a configuration option for FinishSpan.
type FinishOption = ddtrace.FinishOption

// FinishTime sets the given time as the finishing time for the span.
func FinishTime(t time.Time) FinishOption {
	return func(cfg *ddtrace.FinishConfig) {
		cfg.FinishTime = t
	}
}

// WithError adds the given error to the span before marking it as finished.
func WithError(err error) FinishOption {
	return func(cfg *ddtrace.FinishConfig) {
		cfg.Error = err
	}
}
