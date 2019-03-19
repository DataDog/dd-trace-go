package mgo

import (
	"context"
)

type mongoConfig struct {
	ctx           context.Context
	serviceName   string
	analyticsRate float64
}

func newConfig() *mongoConfig {
	return &mongoConfig{
		serviceName: "mongodb",
		ctx:         context.Background(),
		// analyticsRate: globalconfig.AnalyticsRate(),
	}
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

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) DialOption {
	if on {
		return WithAnalyticsRate(1.0)
	}
	return WithAnalyticsRate(0.0)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) DialOption {
	return func(cfg *mongoConfig) {
		cfg.analyticsRate = rate
	}
}
