// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package telemetrytest provides a mock implementation of the telemetry client for testing purposes
package telemetrytest

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/stretchr/testify/mock"
)

// MockClient implements Client and is used for testing purposes outside the telemetry package,
// e.g. the tracer and profiler.
type MockClient struct {
	mock.Mock
	mu              sync.Mutex
	Started         bool
	Configuration   []telemetry.Configuration
	Integrations    []string
	ProfilerEnabled bool
	AsmEnabled      bool
	Metrics         map[telemetry.Namespace]map[string]float64
}

func (c *MockClient) RegisterAppConfig(name string, val interface{}, origin telemetry.Origin) {
	_ = c.Called(name, val, origin)
}

// ProductChange starts and adds configuration data to the mock client.
func (c *MockClient) ProductChange(namespace telemetry.Namespace, enabled bool, configuration []telemetry.Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Started = true
	c.Configuration = append(c.Configuration, configuration...)
	if len(c.Metrics) == 0 {
		c.Metrics = make(map[telemetry.Namespace]map[string]float64)
	}
	c.productChange(namespace, enabled)
}

// ProductStop signals a product has stopped and disables that product in the mock client.
// ProductStop is NOOP for the tracer namespace, since the tracer is not considered a product.
func (c *MockClient) ProductStop(namespace telemetry.Namespace) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if namespace == telemetry.NamespaceTracers {
		return
	}
	c.productChange(namespace, false)
}

// ProductChange signals that a certain product is enabled or disabled for the mock client.
func (c *MockClient) productChange(namespace telemetry.Namespace, enabled bool) {
	switch namespace {
	case telemetry.NamespaceAppSec:
		c.AsmEnabled = enabled
	case telemetry.NamespaceProfilers:
		c.ProfilerEnabled = enabled
	case telemetry.NamespaceTracers:
		return
	default:
		panic("invalid product namespace")
	}
}

// Record stores the value for the given metric. It is currently mocked for `Gauge` and `Distribution` metric types.
func (c *MockClient) Record(ns telemetry.Namespace, _ telemetry.MetricKind, name string, val float64, tags []string, common bool) {
	c.On("Gauge", ns, name, val, tags, common).Return()
	c.On("Record", ns, name, val, tags, common).Return()
	_ = c.Called(ns, name, val, tags, common)
	// record the val for tests that assert based on the value
	if _, ok := c.Metrics[ns]; !ok {
		c.Metrics[ns] = map[string]float64{}
	}
	c.Metrics[ns][name] = val
}

// Count counts the value for the given metric
func (c *MockClient) Count(ns telemetry.Namespace, name string, val float64, tags []string, common bool) {
	c.On("Count", ns, name, val, tags, common).Return()
	_ = c.Called(ns, name, val, tags, common)
}

// Stop is NOOP for the mock client.
func (c *MockClient) Stop() {
}

// ApplyOps is used to record the number of ApplyOps method calls.
func (c *MockClient) ApplyOps(args ...telemetry.Option) {
	c.On("ApplyOps", args).Return()
	_ = c.Called(args)
}

// ConfigChange is a mock for the ConfigChange method.
func (c *MockClient) ConfigChange(args []telemetry.Configuration) {
	c.On("ConfigChange", args).Return()
	_ = c.Called(args)
}
