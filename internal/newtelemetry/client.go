// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"errors"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
)

// NewClient creates a new telemetry client with the given service, environment, and version and config.
func NewClient(service, env, version string, config ClientConfig) (Client, error) {
	if service == "" {
		return nil, errors.New("service name must not be empty")
	}

	if env == "" {
		return nil, errors.New("environment name must not be empty")
	}

	if version == "" {
		return nil, errors.New("version must not be empty")
	}

	config = defaultConfig(config)
	if err := config.validateConfig(); err != nil {
		return nil, err
	}

	tracerConfig := internal.TracerConfig{
		Service: service,
		Env:     env,
		Version: version,
	}

	writerConfig, err := config.ToWriterConfig(tracerConfig)
	if err != nil {
		return nil, err
	}

	writer, err := internal.NewWriter(writerConfig)
	if err != nil {
		return nil, err
	}

	client := &client{
		tracerConfig: tracerConfig,
		writer:       writer,
		payloadQueue: internal.NewRingQueue[transport.Payload](),
	}

	client.dataSources = append(
		client.dataSources,
		&client.integrations,
		&client.products,
		&client.configuration,
	)
	return client, nil
}

type client struct {
	tracerConfig internal.TracerConfig
	writer       internal.Writer
	payloadQueue *internal.RingQueue[transport.Payload]

	// Data sources
	integrations  integrations
	products      products
	configuration configuration

	dataSources []interface {
		Payload() transport.Payload
	}
}

func (c *client) MarkIntegrationAsLoaded(integration Integration) {
	c.integrations.Add(integration)
}

func (c *client) Count(_ types.Namespace, _ string, _ map[string]string) MetricHandle {
	//TODO implement me
	panic("implement me")
}

func (c *client) Rate(_ types.Namespace, _ string, _ map[string]string) MetricHandle {
	//TODO implement me
	panic("implement me")
}

func (c *client) Gauge(_ types.Namespace, _ string, _ map[string]string) MetricHandle {
	//TODO implement me
	panic("implement me")
}

func (c *client) Distribution(_ types.Namespace, _ string, _ map[string]string) MetricHandle {
	//TODO implement me
	panic("implement me")
}

func (c *client) Logger() TelemetryLogger {
	//TODO implement me
	panic("implement me")
}

func (c *client) ProductStarted(product types.Namespace) {
	c.products.Add(product, true, nil)
}

func (c *client) ProductStopped(product types.Namespace) {
	c.products.Add(product, false, nil)
}

func (c *client) ProductStartError(product types.Namespace, err error) {
	c.products.Add(product, false, err)
}

func (c *client) AddAppConfig(key string, value any, origin types.Origin) {
	c.configuration.Add(key, value, origin)
}

func (c *client) AddBulkAppConfig(kvs map[string]any, origin types.Origin) {
	for key, value := range kvs {
		c.configuration.Add(key, value, origin)
	}
}

func (c *client) gatherPayloads() []transport.Payload {
	var res []transport.Payload
	for _, ds := range c.dataSources {
		if payload := ds.Payload(); payload != nil {
			res = append(res, payload)
		}
	}
	return res
}

func (c *client) flush() {
	//TODO implement me
	panic("implement me")
}

func (c *client) appStart() error {
	return nil
}

func (c *client) appStop() {
	//TODO implement me
	panic("implement me")
}

var _ Client = (*client)(nil)
