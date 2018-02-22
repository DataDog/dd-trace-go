package sql

import "github.com/DataDog/dd-trace-go/tracer"

type registerConfig struct {
	serviceName string
	tracer      *tracer.Tracer // TODO(gbbr): Remove this when we switch.
}

// RegisterOption represents an option that can be passed to Register.
type RegisterOption func(*registerConfig)

func defaults(cfg *registerConfig) {
	cfg.tracer = tracer.DefaultTracer
}

// WithServiceName sets the given service name for the registered driver.
func WithServiceName(name string) RegisterOption {
	return func(cfg *registerConfig) {
		cfg.serviceName = name
	}
}

func WithTracer(t *tracer.Tracer) RegisterOption {
	return func(cfg *registerConfig) {
		cfg.tracer = t
	}
}
