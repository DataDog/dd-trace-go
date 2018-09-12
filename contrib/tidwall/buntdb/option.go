package buntdb

import "context"

type config struct {
	serviceName string
	ctx         context.Context
}

func defaults(cfg *config) {
	cfg.serviceName = "buntdb"
	cfg.ctx = context.Background()
}

// An Option customizes the config.
type Option func(cfg *config)

// WithContext sets the context for the transaction.
func WithContext(ctx context.Context) Option {
	return func(cfg *config) {
		cfg.ctx = ctx
	}
}

// WithServiceName sets the given service name for the transaction.
func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}
