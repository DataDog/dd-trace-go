package nsq

import (
	"context"
	"math"

	"github.com/nsqio/go-nsq"
)

// tracer configure
type Config struct {
	*nsq.Config
	service       string
	analyticsRate float64
	ctx           context.Context
}

// tracer configure injector
type Option func(cfg *Config)

// change service name
func WithService(service string) Option {
	return func(cfg *Config) {
		cfg.service = service
	}
}

// change analytics rate
func WithAnalyticsRate(on bool, rate float64) Option {
	return func(cfg *Config) {
		if on && !math.IsNaN(rate) {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// set contexxt into config
func WithContext(ctx context.Context) Option {
	return func(cfg *Config) {
		cfg.ctx = ctx
	}
}

// create new config
func NewConfig(opts ...Option) *Config {
	cfg := &Config{}
	for _, opt := range opts {
		opt(cfg)
	}

	return cfg
}
