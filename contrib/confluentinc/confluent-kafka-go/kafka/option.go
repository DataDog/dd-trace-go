package kafka

import "context"

type config struct {
	serviceName string
	ctx         context.Context
}

// An Option customizes the config.
type Option func(cfg *config)

func newConfig(opts ...Option) *config {
	cfg := &config{
		serviceName: "kafka",
		ctx:         context.Background(),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// WithContext sets the config context to ctx.
func WithContext(ctx context.Context) Option {
	return func(cfg *config) {
		cfg.ctx = ctx
	}
}

// WithServiceName sets the config service name to serviceName.
func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}
