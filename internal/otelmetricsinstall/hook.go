// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package otelmetricsinstall holds registration hooks that let
// ddtrace/opentelemetry/metric wire its OTel metrics functions into
// tracer.Start/Stop without a direct import dependency. The metric package
// registers them in its init() so they fire automatically when imported.
package otelmetricsinstall

import "context"

// StartHook is called by tracer.Start() to install the global OTel meter
// provider and begin collecting Go runtime metrics.
// Nil unless ddtrace/opentelemetry/metric has been imported.
var StartHook func(ctx context.Context) error

// ShutdownHook is called by tracer.Stop() to flush and shut down the global
// OTel MeterProvider installed by StartHook.
// Nil unless ddtrace/opentelemetry/metric has been imported.
var ShutdownHook func(ctx context.Context) error
