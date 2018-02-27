package http

type muxConfig struct{ serviceName string }

// MuxOption represents an option that can be passed to NewServeMux.
type MuxOption func(*muxConfig)

func defaults(cfg *muxConfig) {
	cfg.serviceName = "http.router"
}

// WithServiceName sets the given service name for the returned ServeMux.
func WithServiceName(name string) MuxOption {
	return func(cfg *muxConfig) {
		cfg.serviceName = name
	}
}
