// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gin

import (
	"math"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"

	"github.com/gin-gonic/gin"
)

const defaultServiceName = "gin.router"

type config struct {
	analyticsRate float64
	resourceNamer func(c *gin.Context) string
	serviceName   string
	ignoreRequest func(c *gin.Context) bool
	headerTags    *internal.LockMap
}

func newConfig(serviceName string) *config {
	if serviceName == "" {
		serviceName = namingschema.ServiceName(defaultServiceName)
	}
	rate := globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_GIN_ANALYTICS_ENABLED", false) {
		rate = 1.0
	}
	return &config{
		analyticsRate: rate,
		resourceNamer: defaultResourceNamer,
		serviceName:   serviceName,
		ignoreRequest: func(_ *gin.Context) bool { return false },
		headerTags:    globalconfig.HeaderTagMap(),
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

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) Option {
	headerTagsMap := normalizer.HeaderTagSlice(headers)
	return func(cfg *config) {
		cfg.headerTags = internal.NewLockMap(headerTagsMap)
	}
}

// WithIgnoreRequest specifies a function to use for determining if the
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
