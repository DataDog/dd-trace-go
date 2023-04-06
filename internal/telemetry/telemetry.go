// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.
package telemetry

import "time"

// ProductChange enqueues an app-product-change event that signals a product has been turned on/off.
// The caller can also specify additional configuration changes (e.g. profiler config info), which
// will be sent via the app-client-configuration-change event.
// The enabled field is meant to specify when a product has be enabled/disabled during
// runtime. For example, an app-product-change message with enabled=true can be sent when the profiler
// starts, and another app-product-change message with enabled=false can be sent when the profiler stops.
// Product enablement messages do not apply to the tracer, since the tracer is not considered a product
// by the instrumentation telemetry API.
func (c *client) ProductChange(namespace Namespace, enabled bool, configuration []Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		log("attempted to send product change event, but telemetry client has not started")
		return
	}
	products := new(Products)
	switch namespace {
	case NamespaceProfilers:
		products.Profiler = ProductDetails{Enabled: enabled}
	case NamespaceASM:
		products.AppSec = ProductDetails{Enabled: enabled}
	default:
		log("unknown product namespace, app-product-change telemetry event will not send")
		return
	}
	productReq := c.newRequest(RequestTypeAppProductChange)
	productReq.Body.Payload = products
	c.newRequest(RequestTypeAppClientConfigurationChange)
	if len(configuration) > 0 {
		configChange := new(ConfigurationChange)
		configChange.Configuration = configuration
		configReq := c.newRequest(RequestTypeAppClientConfigurationChange)
		configReq.Body.Payload = configChange
		c.scheduleSubmit(configReq)
	}
	c.scheduleSubmit(productReq)
}

// TimeGauge is used to track a metric that gauges the time (ms) of some portion of code.
// It returns a function that should be called when the desired code finishes executing.
// For example, by adding:
// defer TimeGauge(namespace, "tracer_init_time", nil, true)()
// at the beginning of the tracer Start function, the tracer start time is measured
// and stored as a metric to be flushed by the global telemetry client.
func TimeGauge(namespace Namespace, name string, tags []string, common bool) (finish func()) {
	start := time.Now()
	return func() {
		elapsed := time.Since(start)
		GlobalClient.Gauge(namespace, name, float64(elapsed.Milliseconds()), tags, common)
	}
}
