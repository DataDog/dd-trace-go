package mgo

import "context"

type mongoConfig struct {
	ctx         context.Context
	serviceName string
}

func defaults(cfg *mongoConfig) {
	cfg.serviceName = "mongodb"
	cfg.ctx = context.Background()
}

// MongoOption represents an option that can be passed to Dial
type MongoOption func(*mongoConfig)

// WithServiceName sets the service name for a given MongoDB context.
func WithServiceName(name string) MongoOption {
	return func(cfg *mongoConfig) {
		cfg.serviceName = name
	}
}

// WithContext sets the context.
func WithContext(ctx context.Context) MongoOption {
	return func(cfg *mongoConfig) {
		cfg.ctx = ctx
	}
}
