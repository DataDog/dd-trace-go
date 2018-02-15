package redigo

import "github.com/DataDog/dd-trace-go/tracer"

type dialConfig struct {
	serviceName string
	tracer      *tracer.Tracer // TODO(gbbr): Remove this when we switch.
}

// DialOption represents an option that can be passed to Dial.
type DialOption func(*dialConfig)

func defaults(cfg *dialConfig) {
	cfg.serviceName = "redis.conn"
	cfg.tracer = tracer.DefaultTracer
}

// WithServiceName sets the given service name for the dialled connection.
func WithServiceName(name string) DialOption {
	return func(cfg *dialConfig) {
		cfg.serviceName = name
	}
}

func WithTracer(t *tracer.Tracer) DialOption {
	return func(cfg *dialConfig) {
		cfg.tracer = t
	}
}
