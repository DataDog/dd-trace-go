// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"math"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const (
	defaultServiceName = "http.router"
	ComponentName      = "net/http"
)

type Config struct {
	SpanOpts      []ddtrace.StartSpanOption
	FinishOpts    []ddtrace.FinishOption
	IgnoreRequest func(*http.Request) bool
	ResourceNamer func(*http.Request) string
	IsStatusError func(int) bool
	HeaderTags    *internal.LockMap
	ServiceName   string
	AnalyticsRate float64
}

// Option represents an option that can be passed to NewServeMux or WrapHandler.
type Option func(*Config)

func Default() *Config {
	cfg := new(Config)

	if internal.BoolEnv("DD_TRACE_HTTP_ANALYTICS_ENABLED", false) {
		cfg.AnalyticsRate = 1.0
	} else {
		cfg.AnalyticsRate = globalconfig.AnalyticsRate()
	}
	cfg.ServiceName = namingschema.ServiceName(defaultServiceName)
	cfg.HeaderTags = globalconfig.HeaderTagMap()
	cfg.SpanOpts = []ddtrace.StartSpanOption{tracer.Measured()}
	if !math.IsNaN(cfg.AnalyticsRate) {
		cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.EventSampleRate, cfg.AnalyticsRate))
	}
	cfg.IgnoreRequest = func(_ *http.Request) bool { return false }
	cfg.ResourceNamer = func(_ *http.Request) string { return "" }

	return cfg
}

func With(opts ...Option) *Config {
	cfg := Default()
	for _, fn := range opts {
		fn(cfg)
	}
	return cfg
}
