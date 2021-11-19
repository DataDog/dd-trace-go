// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gin

import (
	"math"
	"net/http"

	"github.com/gin-gonic/gin"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type config struct {
	analyticsRate float64
	resourceNamer func(c *gin.Context) string
	serviceName   string
	ignoreRequest func(c *gin.Context) bool
}

func newConfig(service string) *config {
	if service == "" {
		service = "gin.router"
		if svc := globalconfig.ServiceName(); svc != "" {
			service = svc
		}
	}
	rate := globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_GIN_ANALYTICS_ENABLED", false) {
		rate = 1.0
	}
	return &config{
		analyticsRate: rate,
		resourceNamer: defaultResourceNamer,
		serviceName:   service,
		ignoreRequest: func(_ *gin.Context) bool { return false },
	}
}

// Option specifies instrumentation configuration options.
type Option func(*config)

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

// WithResourceNamer specifies a function which will be used to obtain a resource name for a given
// gin request, using the request's context.
func WithResourceNamer(namer func(c *gin.Context) string) Option {
	return func(cfg *config) {
		cfg.resourceNamer = namer
	}
}

// WithIgnoreRequest holds the function to use for determining if the
// incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(f func(c *gin.Context) bool) Option {
	return func(cfg *config) {
		cfg.ignoreRequest = f
	}
}

func defaultResourceNamer(c *gin.Context) string {
	// getName is a hacky way to check whether *gin.Context implements the FullPath()
	// method introduced in v1.4.0, falling back to the previous implementation otherwise.
	getName := func(req *http.Request, c interface{ HandlerName() string }) string {
		if fp, ok := c.(interface {
			FullPath() string
		}); ok {
			return req.Method + " " + fp.FullPath()
		}
		return c.HandlerName()
	}
	return getName(c.Request, c)
}
