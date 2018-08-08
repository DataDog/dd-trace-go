package mgo

import "context"

type mongoConfig struct {
	ctx         context.Context
	serviceName string
}

// MongoOption represents an option that can be passed to Dial
type MongoOption func(*mongoConfig)

func defaults(cfg *mongoConfig) {
	cfg.serviceName = "mongodb"
}

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
