package leveldb

import (
	"context"
	"math"
)

type config struct {
	ctx           context.Context
	serviceName   string
	analyticsRate float64
}

func newConfig(opts ...Option) *config {
	cfg := &config{
		serviceName: "leveldb",
		ctx:         context.Background(),
		// cfg.analyticsRate: globalconfig.AnalyticsRate(),
		analyticsRate: math.NaN(),
	}
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

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}
