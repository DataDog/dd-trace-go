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
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/mapper"
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

	return newClient(internal.TracerConfig{Service: service, Env: env, Version: version}, config)
}

func newClient(tracerConfig internal.TracerConfig, config ClientConfig) (*client, error) {
	config = defaultConfig(config)
	if err := config.validateConfig(); err != nil {
		return nil, err
	}

	writerConfig, err := newWriterConfig(config, tracerConfig)
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
		clientConfig: config,
		flushMapper:  mapper.NewDefaultMapper(config.HeartbeatInterval),
		// This means that, by default, we incur dataloss if we spend ~30mins without flushing, considering we send telemetry data this looks reasonable.
		// This also means that in the worst case scenario, memory-wise, the app is stabilized after running for 30mins.
		payloadQueue: internal.NewRingQueue[transport.Payload](4, 32),
	}

	client.dataSources = append(client.dataSources,
		&client.integrations,
		&client.products,
		&client.configuration,
	)

	client.flushTicker = internal.NewTicker(func() {
		client.Flush()
	}, config.FlushIntervalRange.Min, config.FlushIntervalRange.Max)

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
	dataSources   []interface {
		Payload() transport.Payload
	}

	flushTicker     *internal.Ticker
	heartbeatTicker *internal.Ticker

	// flushMapper is the transformer to use for the next flush on the gather payloads on this tick
	flushMapper   mapper.Mapper
	flushMapperMu sync.Mutex

	// payloadQueue is used when we cannot flush previously built payloads
	payloadQueue *internal.RingQueue[transport.Payload]
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

func (c *client) Flush() {
	var payloads []transport.Payload
	for _, ds := range c.dataSources {
		if payload := ds.Payload(); payload != nil {
			payloads = append(payloads, payload)
		}
	}

	_, _ = c.flush(payloads)
}

// flush sends all the data sources to the writer by let them flow through the given transformer function.
// The transformer function is used to transform the payloads before sending them to the writer.
func (c *client) flush(payloads []transport.Payload) (int, error) {
	// Transform the payloads
	{
		c.flushMapperMu.Lock()
		payloads, c.flushMapper = c.flushMapper.Transform(payloads)
		c.flushMapperMu.Unlock()
	}

	c.payloadQueue.Enqueue(payloads...)
	payloads = c.payloadQueue.GetBuffer()
	defer c.payloadQueue.ReleaseBuffer(payloads)

	var (
		nbBytes       int
		nonFatalErros []error
	)

	for i, payload := range payloads {
		if payload == nil {
			continue
		}

		nbBytesOfPayload, err := c.writer.Flush(payload)
		if nbBytes > 0 {
			nonFatalErros = append(nonFatalErros, err)
			err = nil
		}

		if err != nil {
			// We stop flushing when we encounter a fatal error, put the payloads in the
			log.Error("error while flushing telemetry data: %v", err)
			c.payloadQueue.Enqueue(payloads[i:]...)
			return nbBytes, err
		}

		if nbBytesOfPayload > c.clientConfig.EarlyFlushPayloadSize {
			// We increase the speed of the flushTicker to try to flush the remaining payloads faster as we are at risk of sending too large payloads to the backend
			c.flushTicker.IncreaseSpeed()
		}

		nbBytes += nbBytesOfPayload
	}

	if len(nonFatalErros) > 0 {
		log.Debug("non-fatal error while flushing telemetry data: %v", errors.Join(nonFatalErros...))
	}

	return nbBytes, nil
}

func (c *client) appStart() {
	c.flushMapperMu.Lock()
	defer c.flushMapperMu.Unlock()

	// Wrap the current flushMapper with the AppStartedMapper so we can add the app-started event to the payloads using available payloads at the time of the call one minute later
	c.flushMapper = mapper.NewAppStartedMapper(c.flushMapper)
}

func (c *client) appStop() {
	c.flushMapperMu.Lock()
	c.flushMapper = mapper.NewAppClosingMapper(c.flushMapper)
	c.flushMapperMu.Unlock()

	// Flush locks the flushMapperMu mutex, so we need to call it outside the lock
	c.Flush()
	c.Close()
}

func (c *client) Close() error {
	c.flushTicker.Stop()
	return nil
}

var _ Client = (*client)(nil)
