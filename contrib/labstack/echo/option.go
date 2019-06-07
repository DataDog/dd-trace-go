package echo

type config struct {
	serviceName   string
	errorHandling bool
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

// WithErrorHandling enables the middleware to call the echo context Error
// method. This is useful to send the correct HTTP status code, as by default
// the error handling is done after the middlewares.
func WithErrorHandling() Option {
	return func(cfg *config) {
		cfg.errorHandling = true
	}
}
