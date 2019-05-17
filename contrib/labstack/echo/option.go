package echo

type config struct {
	serviceName string
}

// MiddlewareOption represents an option that can be passed to Middleware.
type MiddlewareOption func(*config)

func defaults(cfg *config) {
	cfg.serviceName = "echo"
}

// WithServiceName sets the given service name for the system.
func WithServiceName(name string) MiddlewareOption {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}
