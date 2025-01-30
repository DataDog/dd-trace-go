// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package telemetrytest provides a mock implementation of the newtelemetry client for testing purposes
package telemetrytest

import (
	"strings"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry"

	"github.com/stretchr/testify/mock"
)

// MockClient implements Client and is used for testing purposes outside the newtelemetry package,
// e.g. the tracer and profiler.
type MockClient struct {
	mock.Mock
	mu            sync.Mutex
	Started       bool
	Stopped       bool
	Configuration []newtelemetry.Configuration
	Logs          map[newtelemetry.LogLevel]string
	Integrations  []string
	Products      map[newtelemetry.Namespace]bool
	Metrics       map[metricKey]*float64
}

type metricKey struct {
	Namespace newtelemetry.Namespace
	Name      string
	Tags      string
	Kind      string
}

func (m *MockClient) Close() error {
	m.On("Close").Return()
	return m.Called().Error(0)
}

type MockMetricHandle struct {
	mock.Mock
	mu     sync.Mutex
	submit func(ptr *float64, value float64)
	value  *float64
}

func (m *MockMetricHandle) Submit(value float64) {
	m.On("Submit", value).Return()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submit(m.value, value)
}

func (m *MockMetricHandle) Get() float64 {
	m.On("Get").Return()
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.value
}

func (m *MockClient) Count(namespace newtelemetry.Namespace, name string, tags []string) newtelemetry.MetricHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("Count", namespace, name, tags).Return()
	key := metricKey{Namespace: namespace, Name: name, Tags: strings.Join(tags, ","), Kind: "count"}
	if _, ok := m.Metrics[key]; !ok {
		init := 0.0
		m.Metrics[key] = &init
	}

	return &MockMetricHandle{value: m.Metrics[key], submit: func(ptr *float64, value float64) {
		*ptr += value
	}}
}

func (m *MockClient) Rate(namespace newtelemetry.Namespace, name string, tags []string) newtelemetry.MetricHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("Rate", namespace, name, tags).Return()
	key := metricKey{Namespace: namespace, Name: name, Tags: strings.Join(tags, ","), Kind: "rate"}
	if _, ok := m.Metrics[key]; !ok {
		init := 0.0
		m.Metrics[key] = &init
	}

	return &MockMetricHandle{value: m.Metrics[key], submit: func(ptr *float64, value float64) {
		*ptr += value
	}}
}

func (m *MockClient) Gauge(namespace newtelemetry.Namespace, name string, tags []string) newtelemetry.MetricHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("Gauge", namespace, name, tags).Return()
	key := metricKey{Namespace: namespace, Name: name, Tags: strings.Join(tags, ","), Kind: "gauge"}
	if _, ok := m.Metrics[key]; !ok {
		init := 0.0
		m.Metrics[key] = &init
	}

	return &MockMetricHandle{value: m.Metrics[key], submit: func(ptr *float64, value float64) {
		*ptr = value
	}}
}

func (m *MockClient) Distribution(namespace newtelemetry.Namespace, name string, tags []string) newtelemetry.MetricHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("Distribution", namespace, name, tags).Return()
	key := metricKey{Namespace: namespace, Name: name, Tags: strings.Join(tags, ","), Kind: "distribution"}
	if _, ok := m.Metrics[key]; !ok {
		init := 0.0
		m.Metrics[key] = &init
	}

	return &MockMetricHandle{value: m.Metrics[key], submit: func(ptr *float64, value float64) {
		*ptr = value
	}}
}

func (m *MockClient) Log(level newtelemetry.LogLevel, text string, options ...newtelemetry.LogOption) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("Log", level, text, options).Return()
	m.Logs[level] = text
}

func (m *MockClient) ProductStarted(product newtelemetry.Namespace) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("ProductStarted", product).Return()
	m.Products[product] = true
}

func (m *MockClient) ProductStopped(product newtelemetry.Namespace) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("ProductStopped", product).Return()
	m.Products[product] = false
}

func (m *MockClient) ProductStartError(product newtelemetry.Namespace, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("ProductStartError", product, err).Return()
	m.Products[product] = false
}

func (m *MockClient) RegisterAppConfig(key string, value any, origin newtelemetry.Origin) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("RegisterAppConfig", key, value, origin).Return()
	m.Called(key, value, origin)
	m.Configuration = append(m.Configuration, newtelemetry.Configuration{Name: key, Value: value, Origin: origin})
}

func (m *MockClient) RegisterAppConfigs(kvs ...newtelemetry.Configuration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("RegisterAppConfigs", kvs).Return()
	m.Called(kvs)
	m.Configuration = append(m.Configuration, kvs...)
}

func (m *MockClient) MarkIntegrationAsLoaded(integration newtelemetry.Integration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("MarkIntegrationAsLoaded", integration).Return()
	m.Called(integration)
	m.Integrations = append(m.Integrations, integration.Name)
}

func (m *MockClient) Flush() {
	m.On("Flush").Return()
}

func (m *MockClient) AppStart() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("AppStart").Return()
	m.Started = true
}

func (m *MockClient) AppStop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.On("AppStop").Return()
	m.Stopped = true
}

var _ newtelemetry.Client = (*MockClient)(nil)
