package kafka

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type config struct {
	ctx           context.Context
	serviceName   string
	analyticsRate float64
}

// An Option customizes the config.
type Option func(cfg *config)

func newConfig(opts ...Option) *config {
	cfg := &config{
		serviceName:   "kafka",
		ctx:           context.Background(),
		analyticsRate: globalconfig.AnalyticsRate(),
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

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	if on {
		return WithAnalyticsRate(1.0)
	}
	return WithAnalyticsRate(0.0)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		cfg.analyticsRate = rate
	}
}
