// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"errors"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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

	writerConfig, err := NewWriterConfig(config, tracerConfig)
	if err != nil {
		return nil, err
	}

	writer, err := internal.NewWriter(writerConfig)
	if err != nil {
		return nil, err
	}

	client := &client{
		tracerConfig:     tracerConfig,
		writer:           writer,
		clientConfig:     config,
		flushTransformer: internal.MessageBatchTransformer,
	}

	client.ticker = internal.NewTicker(client, config.FlushIntervalRange.Min, config.FlushIntervalRange.Max)
	client.dataSources = append(client.dataSources,
		&client.integrations,
		&client.products,
		&client.configuration,
	)

	return client, nil
}

type client struct {
	tracerConfig internal.TracerConfig
	writer       internal.Writer
	clientConfig ClientConfig

	// Data sources
	integrations  integrations
	products      products
	configuration configuration

	dataSources []interface {
		Payload() transport.Payload
		Size() int
	}

	ticker *internal.Ticker

	// flushTransformer is the transformer to use for the next flush
	flushTransformer   internal.Transformer
	flushTransformerMu sync.Mutex
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

func (c *client) Config() ClientConfig {
	return c.clientConfig
}

func (c *client) Flush() (int, error) {
	var payloads []transport.Payload
	for _, ds := range c.dataSources {
		if payload := ds.Payload(); payload != nil {
			payloads = append(payloads, payload)
		}
	}

	return c.flush(payloads)
}

// flush sends all the data sources to the writer by let them flow through the given wrapper function.
// The wrapper function is used to transform the payloads before sending them to the writer.
func (c *client) flush(payloads []transport.Payload) (int, error) {
	if len(payloads) == 0 {
		return 0, nil
	}

	// Always add the Heartbeat to the payloads
	payloads = append(payloads, transport.AppHeartbeat{})

	// Transform the payloads
	{
		c.flushTransformerMu.Lock()
		payloads, c.flushTransformer = c.flushTransformer.Transform(payloads)
		c.flushTransformerMu.Unlock()
	}

	var (
		nbBytes int
		err     error
	)

	for _, payload := range payloads {
		nbBytesOfPayload, payloadErr := c.writer.Flush(payload)
		if nbBytes > 0 {
			log.Debug("non-fatal error while flushing telemetry data: %v", err)
			err = nil
		}

		nbBytes += nbBytesOfPayload
		err = errors.Join(err, payloadErr)
	}

	return nbBytes, err
}

func (c *client) appStart() {
	c.flushTransformerMu.Lock()
	defer c.flushTransformerMu.Unlock()
	c.flushTransformer = internal.AppStartedTransformer
}

func (c *client) appStop() {
	c.flushTransformerMu.Lock()
	c.flushTransformer = internal.AppClosingTransformer
	c.flushTransformerMu.Unlock()
	c.Flush()
	c.Close()
}

func (c *client) size() int {
	size := 0
	for _, ds := range c.dataSources {
		size += ds.Size()
	}
	return size
}

func (c *client) Close() error {
	c.ticker.Stop()
	return nil
}

var _ Client = (*client)(nil)
