// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package telemetrytest provides a mock implementation of the telemetry client for testing purposes
package telemetrytest

import (
	"github.com/DataDog/dd-trace-go/v2/internal/newtelemetry"

	"github.com/stretchr/testify/mock"
)

// MockClient implements Client and is used for testing purposes outside the telemetry package,
// e.g. the tracer and profiler.
type MockClient struct {
	mock.Mock
}

func (m *MockClient) Close() error {
	return nil
}

type MockMetricHandle struct {
	mock.Mock
}

func (m *MockMetricHandle) Submit(value float64) {
	m.Called(value)
}

func (m *MockMetricHandle) Get() float64 {
	return m.Called().Get(0).(float64)
}

func (m *MockClient) Count(namespace newtelemetry.Namespace, name string, tags []string) newtelemetry.MetricHandle {
	return m.Called(namespace, name, tags).Get(0).(newtelemetry.MetricHandle)
}

func (m *MockClient) Rate(namespace newtelemetry.Namespace, name string, tags []string) newtelemetry.MetricHandle {
	return m.Called(namespace, name, tags).Get(0).(newtelemetry.MetricHandle)
}

func (m *MockClient) Gauge(namespace newtelemetry.Namespace, name string, tags []string) newtelemetry.MetricHandle {
	return m.Called(namespace, name, tags).Get(0).(newtelemetry.MetricHandle)
}

func (m *MockClient) Distribution(namespace newtelemetry.Namespace, name string, tags []string) newtelemetry.MetricHandle {
	return m.Called(namespace, name, tags).Get(0).(newtelemetry.MetricHandle)
}

func (m *MockClient) Log(level newtelemetry.LogLevel, text string, options ...newtelemetry.LogOption) {
	m.Called(level, text, options)
}

func (m *MockClient) ProductStarted(product newtelemetry.Namespace) {
	m.Called(product)
}

func (m *MockClient) ProductStopped(product newtelemetry.Namespace) {
	m.Called(product)
}

func (m *MockClient) ProductStartError(product newtelemetry.Namespace, err error) {
	m.Called(product, err)
}

func (m *MockClient) RegisterAppConfig(key string, value any, origin newtelemetry.Origin) {
	m.Called(key, value, origin)
}

func (m *MockClient) RegisterAppConfigs(kvs ...newtelemetry.Configuration) {
	m.Called(kvs)
}

func (m *MockClient) MarkIntegrationAsLoaded(integration newtelemetry.Integration) {
	m.Called(integration)
}

func (m *MockClient) Flush() {
	m.Called()
}

func (m *MockClient) AppStart() {
	m.Called()
}

func (m *MockClient) AppStop() {
	m.Called()
}

var _ newtelemetry.Client = (*MockClient)(nil)
