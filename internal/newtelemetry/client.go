// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"errors"
	"os"
	"strconv"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/mapper"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
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

	return newClient(internal.TracerConfig{Service: service, Env: env, Version: version}, config)
}

func newClient(tracerConfig internal.TracerConfig, config ClientConfig) (*client, error) {
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
		flushMapper:  mapper.NewDefaultMapper(config.HeartbeatInterval, config.ExtendedHeartbeatInterval),
		payloadQueue: internal.NewRingQueue[transport.Payload](config.PayloadQueueSize.Min, config.PayloadQueueSize.Max),

		dependencies: dependencies{
			DependencyLoader: config.DependencyLoader,
		},
		metrics: metrics{
			skipAllowlist: config.Debug,
		},
		distributions: distributions{
			skipAllowlist: config.Debug,
			queueSize:     config.DistributionsSize,
		},
	}

	client.dataSources = append(client.dataSources,
		&client.integrations,
		&client.products,
		&client.configuration,
		&client.dependencies,
	)

	if config.LogsEnabled {
		client.dataSources = append(client.dataSources, &client.logger)
	}

	if config.MetricsEnabled {
		client.dataSources = append(client.dataSources, &client.metrics, &client.distributions)
	}

	client.flushTicker = internal.NewTicker(func() {
		client.Flush()
	}, config.FlushInterval.Min, config.FlushInterval.Max)

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
	dependencies  dependencies
	logger        logger
	metrics       metrics
	distributions distributions
	dataSources   []interface {
		Payload() transport.Payload
	}

	flushTicker *internal.Ticker

	// flushMapper is the transformer to use for the next flush on the gathered bodies on this tick
	flushMapper   mapper.Mapper
	flushMapperMu sync.Mutex

	// payloadQueue is used when we cannot flush previously built bodies
	payloadQueue *internal.RingQueue[transport.Payload]
}

func (c *client) Log(level LogLevel, text string, options ...LogOption) {
	if !c.clientConfig.LogsEnabled {
		return
	}

	c.logger.Add(level, text, options...)
}

func (c *client) MarkIntegrationAsLoaded(integration Integration) {
	c.integrations.Add(integration)
}

type noopMetricHandle struct{}

func (noopMetricHandle) Submit(_ float64) {}

func (noopMetricHandle) Get() float64 {
	return 0
}

func (c *client) Count(namespace Namespace, name string, tags []string) MetricHandle {
	if !c.clientConfig.MetricsEnabled {
		return noopMetricHandle{}
	}
	return c.metrics.LoadOrStore(namespace, transport.CountMetric, name, tags)
}

func (c *client) Rate(namespace Namespace, name string, tags []string) MetricHandle {
	if !c.clientConfig.MetricsEnabled {
		return noopMetricHandle{}
	}
	return c.metrics.LoadOrStore(namespace, transport.RateMetric, name, tags)
}

func (c *client) Gauge(namespace Namespace, name string, tags []string) MetricHandle {
	if !c.clientConfig.MetricsEnabled {
		return noopMetricHandle{}
	}
	return c.metrics.LoadOrStore(namespace, transport.GaugeMetric, name, tags)
}

func (c *client) Distribution(namespace Namespace, name string, tags []string) MetricHandle {
	if !c.clientConfig.MetricsEnabled {
		return noopMetricHandle{}
	}
	return c.distributions.LoadOrStore(namespace, name, tags)
}

func (c *client) ProductStarted(product Namespace) {
	c.products.Add(product, true, nil)
}

func (c *client) ProductStopped(product Namespace) {
	c.products.Add(product, false, nil)
}

func (c *client) ProductStartError(product Namespace, err error) {
	c.products.Add(product, false, err)
}

func (c *client) RegisterAppConfig(key string, value any, origin Origin) {
	c.configuration.Add(Configuration{key, value, origin})
}

func (c *client) RegisterAppConfigs(kvs ...Configuration) {
	for _, value := range kvs {
		c.configuration.Add(value)
	}
}

func (c *client) Config() ClientConfig {
	return c.clientConfig
}

// Flush sends all the data sources before calling flush
// This function is called by the flushTicker so it should not panic, or it will crash the whole customer application.
// If a panic occurs, we stop the telemetry and log the error.
func (c *client) Flush() {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		log.Warn("panic while flushing telemetry data, stopping telemetry: %v", r)
		if gc, ok := GlobalClient().(*client); ok && gc == c {
			SwapClient(nil)
		}
	}()

	payloads := make([]transport.Payload, 0, 8)
	for _, ds := range c.dataSources {
		if payload := ds.Payload(); payload != nil {
			payloads = append(payloads, payload)
		}
	}

	_, _ = c.flush(payloads)
}

func (c *client) transform(payloads []transport.Payload) []transport.Payload {
	c.flushMapperMu.Lock()
	defer c.flushMapperMu.Unlock()
	payloads, c.flushMapper = c.flushMapper.Transform(payloads)
	return payloads
}

// flush sends all the data sources to the writer by let them flow through the given transformer function.
// The transformer function is used to transform the bodies before sending them to the writer.
func (c *client) flush(payloads []transport.Payload) (int, error) {
	payloads = c.transform(payloads)

	if c.payloadQueue.IsEmpty() && len(payloads) == 0 {
		return 0, nil
	}

	if c.payloadQueue.IsEmpty() {
		c.flushTicker.CanDecreaseSpeed()
	} else if c.payloadQueue.IsFull() {
		c.flushTicker.CanIncreaseSpeed()
	}

	// We enqueue the new payloads to preserve the order of the payloads
	c.payloadQueue.Enqueue(payloads...)
	payloads = c.payloadQueue.Flush()

	var (
		nbBytes        int
		speedIncreased bool
		failedCalls    []internal.EndpointRequestResult
	)

	for i, payload := range payloads {
		results, err := c.writer.Flush(payload)
		c.computeFlushMetrics(results, err)
		if err != nil {
			// We stop flushing when we encounter a fatal error, put the bodies in the queue and return the error
			log.Error("error while flushing telemetry data: %v", err)
			c.payloadQueue.Enqueue(payloads[i:]...)
			return nbBytes, err
		}

		failedCalls = append(failedCalls, results[:len(results)-1]...)
		successfulCall := results[len(results)-1]

		if !speedIncreased && successfulCall.PayloadByteSize > c.clientConfig.EarlyFlushPayloadSize {
			// We increase the speed of the flushTicker to try to flush the remaining bodies faster as we are at risk of sending too large bodies to the backend
			c.flushTicker.CanIncreaseSpeed()
			speedIncreased = true
		}

		nbBytes += successfulCall.PayloadByteSize
	}

	if len(failedCalls) > 0 {
		errName := "error"
		if len(failedCalls) > 1 {
			errName = "errors"
		}
		var errs []error
		for _, call := range failedCalls {
			errs = append(errs, call.Error)
		}
		log.Debug("non-fatal %s while flushing telemetry data: %v", errName, errors.Join(errs...))
	}

	return nbBytes, nil
}

// computeFlushMetrics computes and submits the metrics for the flush operation using the output from the writer.Flush method.
// It will submit the number of requests, responses, errors, the number of bytes sent and the duration of the call that was successful.
func (c *client) computeFlushMetrics(results []internal.EndpointRequestResult, err error) {
	if !c.clientConfig.InternalMetricsEnabled {
		return
	}

	indexToEndpoint := func(i int) string {
		if i == 0 {
			return "agent"
		}
		return "agentless"
	}

	for i, result := range results {
		c.Count(transport.NamespaceTelemetry, "telemetry_api.requests", []string{"endpoint:" + indexToEndpoint(i)}).Submit(1)
		if result.StatusCode != 0 {
			c.Count(transport.NamespaceTelemetry, "telemetry_api.responses", []string{"endpoint:" + indexToEndpoint(i), "status_code:" + strconv.Itoa(result.StatusCode)}).Submit(1)
		}

		if result.Error != nil {
			typ := "network"
			if os.IsTimeout(result.Error) {
				typ = "timeout"
			}
			var writerStatusCodeError *internal.WriterStatusCodeError
			if errors.As(result.Error, &writerStatusCodeError) {
				typ = "status_code"
			}
			c.Count(transport.NamespaceTelemetry, "telemetry_api.errors", []string{"endpoint:" + indexToEndpoint(i), "type:" + typ}).Submit(1)
		}
	}

	if err != nil {
		return
	}

	successfulCall := results[len(results)-1]
	endpoint := indexToEndpoint(len(results) - 1)
	c.Distribution(transport.NamespaceTelemetry, "telemetry_api.bytes", []string{"endpoint:" + endpoint}).Submit(float64(successfulCall.PayloadByteSize))
	c.Distribution(transport.NamespaceTelemetry, "telemetry_api.ms", []string{"endpoint:" + endpoint}).Submit(float64(successfulCall.CallDuration.Milliseconds()))
}

func (c *client) AppStart() {
	c.flushMapperMu.Lock()
	defer c.flushMapperMu.Unlock()
	c.flushMapper = mapper.NewAppStartedMapper(c.flushMapper)
}

func (c *client) AppStop() {
	c.flushMapperMu.Lock()
	defer c.flushMapperMu.Unlock()
	c.flushMapper = mapper.NewAppClosingMapper(c.flushMapper)
}

func (c *client) Close() error {
	c.flushTicker.Stop()
	return nil
}
