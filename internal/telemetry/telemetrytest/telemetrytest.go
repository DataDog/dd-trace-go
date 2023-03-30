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

type MockClient struct {
	mu              sync.Mutex
	Started         bool
	Configuration   []telemetry.Configuration
	ProfilerEnabled bool
}

func (c *MockClient) Start(configuration []telemetry.Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Started = true
	c.Configuration = append(c.Configuration, configuration...)
}

func (c *MockClient) ProductChange(namespace telemetry.Namespace, enabled bool, configuration []telemetry.Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ProfilerEnabled = (namespace == telemetry.NamespaceProfilers) && enabled
	c.Configuration = append(c.Configuration, configuration...)
}

func (c *MockClient) Gauge(namespace telemetry.Namespace, name string, value float64, tags []string, common bool) {
}
func (c *MockClient) Count(namespace telemetry.Namespace, name string, value float64, tags []string, common bool) {
}
func (c *MockClient) Stop() {
}
func (c *MockClient) ApplyOps(ops ...telemetry.Option) {
}
