package nsq

import (
	"context"

	"github.com/nsqio/go-nsq"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

const (
	spanTypeProducer spanType = ext.SpanTypeMessageProducer
	spanTypeConsumer          = ext.SpanTypeMessageConsumer
)

type spanType string

type Config struct {
	*nsq.Config
	service string
	// analyticsRate float64
	ctx context.Context
}

type Option func(cfg *Config)

func WithService(service string) Option {
	return func(cfg *Config) {
		cfg.service = service
	}
}

// func WithAnalyticsRate(on bool, rate float64) Option {
// 	return func(cfg *Config) {
// 		if on && !math.IsNaN(rate) {
// 			cfg.analyticsRate = rate
// 		} else {
// 			cfg.analyticsRate = math.NaN()
// 		}
// 	}
// }

func WithContext(ctx context.Context) Option {
	return func(cfg *Config) {
		cfg.ctx = ctx
	}
}

func NewConfig(opts ...Option) *Config {
	cfg := &Config{}
	for _, opt := range opts {
		opt(cfg)
	}

	return cfg
}
