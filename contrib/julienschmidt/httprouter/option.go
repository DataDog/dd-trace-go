// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httprouter

import (
	"github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2/internal/tracing"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const defaultServiceName = "http.router"

type routerConfig struct {
	serviceName   string
	spanOpts      []tracer.StartSpanOption
	analyticsRate float64
	headerTags    instrumentation.HeaderTags
}

// RouterOption represents an option that can be passed to New.
type RouterOption = tracing.Option

// WithService sets the given service name for the returned router.
var WithService = tracing.WithService

// WithSpanOptions applies the given set of options to the span started by the router.
var WithSpanOptions = tracing.WithSpanOptions

// WithAnalytics enables Trace Analytics for all started spans.
var WithAnalytics = tracing.WithAnalytics

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
var WithAnalyticsRate = tracing.WithAnalyticsRate

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
var WithHeaderTags = tracing.WithHeaderTags
