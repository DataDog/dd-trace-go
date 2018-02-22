package grpc

import "github.com/DataDog/dd-trace-go/tracer"

type interceptorConfig struct {
	serviceName string
	tracer      *tracer.Tracer // TODO(gbbr): Remove this when we switch.
}

// InterceptorOption represents an option that can be passed to the grpc unary
// client and server interceptors.
type InterceptorOption func(*interceptorConfig)

func defaults(cfg *interceptorConfig) {
	cfg.tracer = tracer.DefaultTracer
}

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) InterceptorOption {
	return func(cfg *interceptorConfig) {
		cfg.serviceName = name
	}
}

func WithTracer(t *tracer.Tracer) InterceptorOption {
	return func(cfg *interceptorConfig) {
		cfg.tracer = t
	}
}
