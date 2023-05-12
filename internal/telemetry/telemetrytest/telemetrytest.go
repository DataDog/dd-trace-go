// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package telemetrytest provides a mock implementation of the telemetry client for testing purposes
package telemetrytest

import (
	"sync"

	"github.com/stretchr/testify/mock"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

// MockClient implements Client and is used for testing purposes outside of the telemetry package,
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

// ProductStart starts and adds configuration data to the mock client.
func (c *MockClient) ProductStart(namespace telemetry.Namespace, configuration []telemetry.Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Started = true
	c.Configuration = append(c.Configuration, configuration...)
	if len(c.Metrics) == 0 {
		c.Metrics = make(map[telemetry.Namespace]map[string]float64)
	}
	c.productChange(namespace, true)
	if namespace == telemetry.NamespaceTracers {
		c.productChange(telemetry.NamespaceASM, true)
	}
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
	case telemetry.NamespaceASM:
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

// ApplyOps is NOOP for the mock client.
func (c *MockClient) ApplyOps(_ ...telemetry.Option) {
}
