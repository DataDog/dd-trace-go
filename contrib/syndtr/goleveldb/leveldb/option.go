package leveldb

import "context"

type config struct {
	serviceName string
	ctx         context.Context
}

func newConfig(opts ...Option) *config {
	cfg := new(config)
	cfg.serviceName = "leveldb"
	cfg.ctx = context.Background()
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// Option represents an option that can be used customize the db tracing config.
type Option func(*config)

// WithContext sets the tracing context for the db.
func WithContext(ctx context.Context) Option {
	return func(cfg *config) {
		cfg.ctx = ctx
	}
}

// WithServiceName sets the given service name for the db.
func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}
