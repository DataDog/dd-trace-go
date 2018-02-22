package gocql

import "github.com/DataDog/dd-trace-go/tracer"

type queryConfig struct {
	serviceName string
	tracer      *tracer.Tracer // TODO(gbbr): Remove this when we switch.
}

// WrapOption represents an option that can be passed to WrapQuery.
type WrapOption func(*queryConfig)

func defaults(cfg *queryConfig) {
	cfg.serviceName = "gocql.query"
	cfg.tracer = tracer.DefaultTracer
}

// WithServiceName sets the given service name for the returned query.
func WithServiceName(name string) WrapOption {
	return func(cfg *queryConfig) {
		cfg.serviceName = name
	}
}

func WithTracer(t *tracer.Tracer) WrapOption {
	return func(cfg *queryConfig) {
		cfg.tracer = t
	}
}
