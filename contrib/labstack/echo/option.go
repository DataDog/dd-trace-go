package echo

type config struct {
	serviceName string
}

// Option represents an option that can be passed to Middleware.
type Option func(*config)

func defaults(cfg *config) {
	cfg.serviceName = "echo"
}

// WithServiceName sets the given service name for the system.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}
