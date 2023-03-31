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

// Start starts and adds configuration data to the mock client.
func (c *MockClient) Start(configuration []telemetry.Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Started = true
	c.Configuration = append(c.Configuration, configuration...)
}

// ProductChange signals that a certain product is enabled or disabled for the mock client.
func (c *MockClient) ProductChange(namespace telemetry.Namespace, enabled bool, configuration []telemetry.Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch namespace {
	case telemetry.NamespaceASM:
		c.AsmEnabled = enabled
	case telemetry.NamespaceProfilers:
		c.ProfilerEnabled = enabled
	default:
		panic("invalid product namespace")
	}
	c.Configuration = append(c.Configuration, configuration...)
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
