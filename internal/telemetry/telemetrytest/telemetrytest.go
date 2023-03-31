// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package telemetrytest provides a mock implementation of the telemetry client for testing purposes
package telemetrytest

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

// MockClient implements Client and is used for testing purposes.
type MockClient struct {
	mu              sync.Mutex
	Started         bool
	Configuration   []telemetry.Configuration
	ProfilerEnabled bool
	AsmEnabled      bool
}

// ProductStart starts and adds configuration data to the mock client.
func (c *MockClient) ProductStart(namespace telemetry.Namespace, configuration []telemetry.Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Started = true
	c.Configuration = append(c.Configuration, configuration...)
	c.productChange(namespace, true, nil)
}

// ProductStop signals a product has stopped and disables that product in the mock client.
// ProductStop is NOOP for the tracer namespace, since the tracer is not considered a product.
func (c *MockClient) ProductStop(namespace telemetry.Namespace) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if namespace == telemetry.NamespaceTracers {
		return
	}
	c.productChange(namespace, false, nil)
}

// ProductChange signals that a certain product is enabled or disabled for the mock client.
func (c *MockClient) productChange(namespace telemetry.Namespace, enabled bool, configuration []telemetry.Configuration) {
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

// Gauge is NOOP for the mock client.
func (c *MockClient) Gauge(namespace telemetry.Namespace, name string, value float64, tags []string, common bool) {
}

// Count is NOOP for the mock client.
func (c *MockClient) Count(namespace telemetry.Namespace, name string, value float64, tags []string, common bool) {
}

// Stop is NOOP for the mock client.
func (c *MockClient) Stop() {
}

// ApplyOps is NOOP for the mock client.
func (c *MockClient) ApplyOps(ops ...telemetry.Option) {
}
