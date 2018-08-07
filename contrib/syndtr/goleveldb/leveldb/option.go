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

type Option func(*config)

func WithContext(ctx context.Context) Option {
	return func(cfg *config) {
		cfg.ctx = ctx
	}
}

func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}
