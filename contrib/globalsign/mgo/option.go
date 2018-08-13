package mgo

import "context"

type mongoConfig struct {
	ctx         context.Context
	serviceName string
	tags        map[string]string
}

func defaults(cfg *mongoConfig) {
	cfg.serviceName = "mongodb"
	cfg.ctx = context.Background()
	cfg.tags = make(map[string]string)
}

// DialOption represents an option that can be passed to Dial
type DialOption func(*mongoConfig)

// WithServiceName sets the service name for a given MongoDB context.
func WithServiceName(name string) DialOption {
	return func(cfg *mongoConfig) {
		cfg.serviceName = name
	}
}

// WithContext sets the context.
func WithContext(ctx context.Context) DialOption {
	return func(cfg *mongoConfig) {
		cfg.ctx = ctx
	}
}
