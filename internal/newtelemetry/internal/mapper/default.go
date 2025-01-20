// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mapper

import (
	"time"

	"golang.org/x/time/rate"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

// NewDefaultMapper returns a Mapper that transforms payloads into a MessageBatch and adds a heartbeat message.
// The heartbeat message is added every heartbeatInterval.
func NewDefaultMapper(heartbeatInterval time.Duration) Mapper {
	return &defaultMapper{
		heartbeatEnricher: heartbeatEnricher{
			rateLimiter: rate.NewLimiter(rate.Every(heartbeatInterval), 1),
		},
	}
}

type defaultMapper struct {
	heartbeatEnricher
	messageBatchReducer
}

func (t *defaultMapper) Transform(payloads []transport.Payload) ([]transport.Payload, Mapper) {
	payloads, _ = t.heartbeatEnricher.Transform(payloads)
	payloads, _ = t.messageBatchReducer.Transform(payloads)
	return payloads, t
}

type messageBatchReducer struct{}

func (t *messageBatchReducer) Transform(payloads []transport.Payload) ([]transport.Payload, Mapper) {
	if len(payloads) <= 1 {
		return payloads, t
	}

	messages := make([]transport.Message, len(payloads))
	for _, payload := range payloads {
		messages = append(messages, transport.Message{
			Payload:     payload,
			RequestType: payload.RequestType(),
		})
	}

	return []transport.Payload{transport.MessageBatch{Payload: messages}}, t
}

type heartbeatEnricher struct {
	rateLimiter *rate.Limiter
}

func (t *heartbeatEnricher) Transform(payloads []transport.Payload) ([]transport.Payload, Mapper) {
	if !t.rateLimiter.Allow() {
		return payloads, t
	}

	extendedHeartbeat := transport.AppExtendedHeartbeat{}
	payloadLefts := make([]transport.Payload, 0, len(payloads))
	for _, payload := range payloads {
		switch payload.(type) {
		case transport.AppDependenciesLoaded:
			extendedHeartbeat.Dependencies = payload.(transport.AppDependenciesLoaded).Dependencies
		case transport.AppIntegrationChange:
			extendedHeartbeat.Integrations = payload.(transport.AppIntegrationChange).Integrations
		case transport.AppClientConfigurationChange:
			extendedHeartbeat.Configuration = payload.(transport.AppClientConfigurationChange).Configuration
		default:
			payloadLefts = append(payloadLefts, payload)
		}
	}

	if len(payloadLefts) == len(payloads) {
		// No Payloads were consumed by the extended heartbeat, we can add a regular heartbeat
		return append(payloads, transport.AppHeartbeat{}), t
	}

	return append(payloadLefts, extendedHeartbeat), t
}
