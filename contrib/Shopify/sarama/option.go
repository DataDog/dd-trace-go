package sarama

type config struct {
	serviceName string
}

func defaults(cfg *config) {
	cfg.serviceName = "kafka"
}

// An Option is used to customize the config for the sarama tracer.
type Option func(cfg *config)

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}
