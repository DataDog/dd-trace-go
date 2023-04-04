// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.
package telemetry

import "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

// ProductStart signals that the product has started with some configuration
// information. It will start the telemetry client if it is not already started. If it is
// already started, it will send any necessary app-product-change events to
// indicate whether the product is enabled.
func (c *client) ProductStart(namespace Namespace, configuration []Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		c.configChange(configuration)
		switch namespace {
		case NamespaceProfilers:
			c.productChange(NamespaceProfilers, true)
		case NamespaceTracers:
			// Since appsec is integrated with the tracer, we sent an app-product-change
			// update about appsec when the tracer starts. Any tracer-related configuration
			// information can be passed along here as well.
			if appsec.Enabled() {
				c.productChange(NamespaceASM, false)
			}
		case NamespaceASM:
			c.productChange(NamespaceASM, true)
		default:
			log("unknown product namespace provided to ProductStart")
		}
	} else {
		c.start(configuration, namespace)
	}
}

// ProductStop signals that a Product had stopped. For the tracer, we do nothing when it stops since
// it is not considered a Product by the telemetry API.
// Ensure you have called ProductStart before calling ProductStop.
func (c *client) ProductStop(namespace Namespace) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if namespace == NamespaceTracers {
		return
	}
	c.productChange(namespace, false)
}

// configChange enqeues an app-client-configuration-change event to be flushed.
// Must be called with c.mu locked.
func (c *client) configChange(configuration []Configuration) {
	if !c.started {
		log("attempted to send config change event, but telemetry client has not started")
		return
	}
	if len(configuration) > 0 {
		configChange := new(ConfigurationChange)
		configChange.Configuration = configuration
		configReq := c.newRequest(RequestTypeAppClientConfigurationChange)
		configReq.Body.Payload = configChange
		c.scheduleSubmit(configReq)
	}
}

// productChange enqueues an app-product-change event that signals a product has been turned on/off.
// Must be called with c.mu locked.
// The enabled field is meant to specify when a product has be enabled/disabled during
// runtime. For example, an app-product-change message with enabled=true can be sent when the profiler
// starts, and another app-product-change message with enabled=false can be sent when the profiler stops.
// Product enablement messages do not apply to the tracer, since the tracer is not considered a product
// by the instrumentation telemetry API.
func (c *client) productChange(namespace Namespace, enabled bool) {
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
	c.scheduleSubmit(productReq)
}
