package http

import "github.com/DataDog/dd-trace-go/tracer"

type muxConfig struct {
	serviceName string
	tracer      *tracer.Tracer // TODO(gbbr): Remove this when we switch.
}

// MuxOption represents an option that can be passed to NewServeMux.
type MuxOption func(*muxConfig)

func defaults(cfg *muxConfig) {
	cfg.serviceName = "http.router"
	cfg.tracer = tracer.DefaultTracer
}

// WithServiceName sets the given service name for the returned ServeMux.
func WithServiceName(name string) MuxOption {
	return func(cfg *muxConfig) {
		cfg.serviceName = name
	}
}

func WithTracer(t *tracer.Tracer) MuxOption {
	return func(cfg *muxConfig) {
		cfg.tracer = t
	}
}
