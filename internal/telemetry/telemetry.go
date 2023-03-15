// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.
package telemetry

import (
	"runtime/debug"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

// Start registers that the app has begun running with the app-started event
// Start also configures the telemetry client based on the following telemetry
// environment variables: DD_INSTRUMENTATION_TELEMETRY_ENABLED,
// DD_TELEMETRY_HEARTBEAT_INTERVAL, DD_INSTRUMENTATION_TELEMETRY_DEBUG,
// and DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED.
// TODO: implement passing in error information about tracer start
func (c *Client) Start(configuration []Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if Disabled() {
		return
	}
	if c.started {
		c.log("attempted to start telemetry client when client has already started - ignoring attempt")
		return
	}
	c.applyFallbackOps()

	c.started = true

	c.debug = internal.BoolEnv("DD_INSTRUMENTATION_TELEMETRY_DEBUG", false)

	payload := &AppStarted{
		Configuration: append([]Configuration{}, configuration...),
		Products: Products{
			AppSec: ProductDetails{
				Version: version.Tag,
				Enabled: appsec.Enabled(),
			},
		},
	}

	appStarted := c.newRequest(RequestTypeAppStarted)
	appStarted.Body.Payload = payload
	c.scheduleSubmit(appStarted)

	if collectDependencies() {
		var depPayload Dependencies
		if deps, ok := debug.ReadBuildInfo(); ok {
			for _, dep := range deps.Deps {
				depPayload.Dependencies = append(depPayload.Dependencies,
					Dependency{
						Name:    dep.Path,
						Version: dep.Version,
					},
				)
			}
		}
		dep := c.newRequest(RequestTypeDependenciesLoaded)
		dep.Body.Payload = depPayload
		c.scheduleSubmit(dep)
	}

	c.flush()

	heartbeat := internal.IntEnv("DD_TELEMETRY_HEARTBEAT_INTERVAL", defaultHeartbeatInterval)
	if heartbeat < 1 || heartbeat > 3600 {
		c.log("DD_TELEMETRY_HEARTBEAT_INTERVAL=%d not in [1,3600] range, setting to default of %d", heartbeat, defaultHeartbeatInterval)
		heartbeat = defaultHeartbeatInterval
	}
	c.heartbeatInterval = time.Duration(heartbeat) * time.Second
	c.heartbeatT = time.AfterFunc(c.heartbeatInterval, c.backgroundHeartbeat)
}

// Stop notifies the telemetry endpoint that the app is closing. All outstanding
// messages will also be sent. No further messages will be sent until the client
// is started again
func (c *Client) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	c.started = false
	c.heartbeatT.Stop()
	// close request types have no body
	r := c.newRequest(RequestTypeAppClosing)
	c.scheduleSubmit(r)
	c.flush()
}

// backgroundHeartbeat is invoked at every heartbeat interval,
// sending the app-heartbeat event and flushing any outstanding
// telemetry messages
func (c *Client) backgroundHeartbeat() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	c.scheduleSubmit(c.newRequest(RequestTypeAppHeartbeat))
	c.flush()
	c.heartbeatT.Reset(c.heartbeatInterval)
}

// ProductChange enqueues an app-product-change event that signals a product has been turned on/off.
// the caller can also specify additional configuration changes (e.g. profiler config info),
// which will be sent via the app-client-configuration-change event
func (c *Client) ProductChange(namespace Namespace, enabled bool, configuration []Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		c.log("attempted to send product change event, but telemetry client has not started")
		return
	}
	products := new(Products)
	switch namespace {
	case NamespaceProfilers:
		products.Profiler = ProductDetails{Enabled: enabled}
	case NamespaceASM:
		products.AppSec = ProductDetails{Enabled: enabled}
	default:
		c.log("unknown product namespace, app-product-change telemetry event will not send")
		return
	}
	productReq := c.newRequest(RequestTypeAppProductChange)
	productReq.Body.Payload = products
	c.newRequest(RequestTypeAppClientConfigurationChange)
	if len(configuration) > 0 {
		configChange := new(ConfigurationChange)
		configChange.Configuration = append([]Configuration{}, configuration...)
		configReq := c.newRequest(RequestTypeAppClientConfigurationChange)
		configReq.Body.Payload = configChange
		c.scheduleSubmit(configReq)
	}
	c.scheduleSubmit(productReq)
}

type metricKind string

var (
	metricKindGauge metricKind = "gauge"
	metricKindCount metricKind = "count"
)

type metric struct {
	name  string
	kind  metricKind
	value float64
	// Unix timestamp
	ts     float64
	tags   []string
	common bool
}

// TODO: Can there be identically named/tagged metrics with a "common" and "not
// common" variant?

func newmetric(name string, kind metricKind, tags []string, common bool) *metric {
	return &metric{
		name:   name,
		kind:   kind,
		tags:   append([]string{}, tags...),
		common: common,
	}
}

func metricKey(name string, tags []string) string {
	return name + strings.Join(tags, "-")
}

// Gauge sets the value for a gauge with the given name and tags. If the metric
// is not language-specific, common should be set to true
func (c *Client) Gauge(namespace Namespace, name string, value float64, tags []string, common bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	if _, ok := c.metrics[namespace]; !ok {
		c.metrics[namespace] = map[string]*metric{}
	}
	key := metricKey(name, tags)
	m, ok := c.metrics[namespace][key]
	if !ok {
		m = newmetric(name, metricKindGauge, tags, common)
		c.metrics[namespace][key] = m
	}
	m.value = value
	m.ts = float64(time.Now().Unix())
	c.newMetrics = true
}

// Count adds the value to a count with the given name and tags. If the metric
// is not language-specific, common should be set to true
func (c *Client) Count(namespace Namespace, name string, value float64, tags []string, common bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	if _, ok := c.metrics[namespace]; !ok {
		c.metrics[namespace] = map[string]*metric{}
	}
	key := metricKey(name, tags)
	m, ok := c.metrics[namespace][key]
	if !ok {
		m = newmetric(name, metricKindCount, tags, common)
		c.metrics[namespace][key] = m
	}
	m.value += value
	m.ts = float64(time.Now().Unix())
	c.newMetrics = true
}
