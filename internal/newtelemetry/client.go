// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
)

// NewClient creates a new telemetry client with the given service, environment, and version and config.
func NewClient(service, env, version string, config ClientConfig) (Client, error) {
	return nil, nil
}

type client struct{}

func (c client) MarkIntegrationAsLoaded(integration Integration) {
	//TODO implement me
	panic("implement me")
}

func (c client) Count(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	//TODO implement me
	panic("implement me")
}

func (c client) Rate(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	//TODO implement me
	panic("implement me")
}

func (c client) Gauge(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	//TODO implement me
	panic("implement me")
}

func (c client) Distribution(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	//TODO implement me
	panic("implement me")
}

func (c client) Logger() TelemetryLogger {
	//TODO implement me
	panic("implement me")
}

func (c client) ProductStarted(product types.Namespace) {
	//TODO implement me
	panic("implement me")
}

func (c client) ProductStopped(product types.Namespace) {
	//TODO implement me
	panic("implement me")
}

func (c client) ProductStartError(product types.Namespace, err error) {
	//TODO implement me
	panic("implement me")
}

func (c client) AddAppConfig(key string, value any, origin types.Origin) {
	//TODO implement me
	panic("implement me")
}

func (c client) AddBulkAppConfig(kvs map[string]any, origin types.Origin) {
	//TODO implement me
	panic("implement me")
}

func (c client) flush() {
	//TODO implement me
	panic("implement me")
}

func (c client) appStart() error {
	//TODO implement me
	panic("implement me")
}

func (c client) appStop() {
	//TODO implement me
	panic("implement me")
}

var _ Client = (*client)(nil)
