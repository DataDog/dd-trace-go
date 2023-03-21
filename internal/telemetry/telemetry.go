// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.
package telemetry

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
)

// ProductStart signals that the tracer or profiler has started with
// some configuration information. The telemetry event emitted depends
// on which product is starting, as well as whether an app-started event
// has already been sent.
func ProductStart(namespace Namespace, configuration []Configuration) {
	GlobalClient.mu.Lock()
	defer GlobalClient.mu.Unlock()

	if GlobalClient.started {
		switch namespace {
		case NamespaceProfilers:
			GlobalClient.productChange(NamespaceProfilers, true, configuration)
		case NamespaceTracers:
			// Since appsec is integrated with the tracer, we sent an app-product-change
			// update about appsec when the tracer starts. Any tracer-related configuration
			// information can be passed along here as well.
			GlobalClient.productChange(NamespaceASM, appsec.Enabled(), configuration)
		}
	} else {
		GlobalClient.start(configuration, namespace)
	}

}

// ProductStop signals that a Product had stopped. For the tracer, we do nothing when it stops.
// Ensure you have called ProductStart before calling ProductStop.
func ProductStop(namespace Namespace) {
	GlobalClient.mu.Lock()
	defer GlobalClient.mu.Unlock()
	if namespace == NamespaceTracers {
		return
	}
	GlobalClient.productChange(namespace, false, []Configuration{})
}

// ProductChange enqueues an app-product-change event that signals a product has been turned on/off.
// The caller can also specify additional configuration changes (e.g. profiler config info), which
// will be sent via the app-client-configuration-change event.
// The enabled field is meant to specify when a product has be enabled/disabled during
// runtime. For example, an app-product-change message with enabled=true can be sent when the profiler
// starts, and another app-product-change message with enabled=false can be sent when the profiler stops.
// Product enablement messages do not apply to the tracer, since the tracer is not considered a product
// by the instrumentation telemetry API.
func (c *Client) productChange(namespace Namespace, enabled bool, configuration []Configuration) {
	products := new(Products)
	switch namespace {
	case NamespaceProfilers:
		products.Profiler = ProductDetails{Enabled: enabled}
	case NamespaceASM:
		products.AppSec = ProductDetails{Enabled: enabled}
	case NamespaceTracers:
		c.log("attempted to send app-product-change with the tracer namespace, but tracer is not a product")
		return
	default:
		c.log("unknown product namespace, app-product-change telemetry event will not send")
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
