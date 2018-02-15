package redis

import "github.com/DataDog/dd-trace-go/tracer"

type clientConfig struct {
	serviceName string
	tracer      *tracer.Tracer // TODO(gbbr): Remove this when we switch.
}

// ClientOption represents an option that can be used to create or wrap a client.
type ClientOption func(*clientConfig)

func defaults(cfg *clientConfig) {
	cfg.tracer = tracer.DefaultTracer
	cfg.serviceName = "redis.client"
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}

func WithTracer(t *tracer.Tracer) ClientOption {
	return func(cfg *clientConfig) {
		cfg.tracer = t
	}
}
