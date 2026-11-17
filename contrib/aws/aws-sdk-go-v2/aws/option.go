// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type config struct {
	serviceName   string
	serviceSource string
	analyticsRate float64
	errCheck      func(err error) bool

	injectKinesis     bool
	injectEventBridge bool
	injectSQS         bool
	injectSNS         bool
	injectSFN         bool

	dataStreamsEnabled bool
}

// Option describes options for the AWS integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to AppendMiddleware.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.analyticsRate = instr.AnalyticsRate(false)
	// Turning injection off only skips writing the payload. The span and the Data Streams
	// checkpoint are still recorded.
	cfg.injectKinesis = instr.KinesisInjectDatadogAttributeEnabled()
	cfg.injectEventBridge = instr.EventBridgeInjectDatadogAttributeEnabled()
	cfg.injectSQS = instr.SQSInjectDatadogAttributeEnabled()
	cfg.injectSNS = instr.SNSInjectDatadogAttributeEnabled()
	cfg.injectSFN = instr.SFNInjectDatadogAttributeEnabled()
	cfg.dataStreamsEnabled = instr.DataStreamsEnabled()
}

// WithService sets the given service name for the dialled connection.
// When the service name is not explicitly set it will be inferred based on the
// request to AWS.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithDataStreams enables Data Streams Monitoring for SQS in this integration, as the Kafka integrations'
// WithDataStreams does: https://www.datadoghq.com/product/data-streams-monitoring/
// The tracer records checkpoints only when its Data Streams processor runs, which requires
// DD_DATA_STREAMS_ENABLED=true at tracer start. That setting also turns this option on, so the option
// has no effect on its own.
// SendMessage and SendMessageBatch set a produce checkpoint and add the pathway to each message's
// _datadog attribute. ReceiveMessage asks for the _datadog attribute, sets a consume checkpoint for each
// message, and writes the new pathway back into the message's _datadog attribute, where
// datastreams.ExtractFromBase64Carrier can read it. The pathway is not written back to a message that
// already has the SQS maximum of 10 attributes.
func WithDataStreams() OptionFn {
	return func(cfg *config) {
		cfg.dataStreamsEnabled = true
	}
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever an aws operation
// finishes with an error.
func WithErrorCheck(fn func(err error) bool) OptionFn {
	return func(cfg *config) {
		cfg.errCheck = fn
	}
}
