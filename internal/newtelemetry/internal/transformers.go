// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

// Transformer is an interface for transforming payloads to comply with different types of lifecycle events in the application.
type Transformer interface {
	// Transform transforms the given payloads and returns the transformed payloads and the Transformer to use for the next
	// transformation.
	Transform([]transport.Payload) ([]transport.Payload, Transformer)
}

type appStartedTransformer struct{}

var AppStartedTransformer Transformer = &appStartedTransformer{}

func (t *appStartedTransformer) Transform(payloads []transport.Payload) ([]transport.Payload, Transformer) {
	appStarted := transport.AppStarted{
		InstallSignature: transport.InstallSignature{
			InstallID:   globalconfig.InstrumentationInstallID(),
			InstallType: globalconfig.InstrumentationInstallType(),
			InstallTime: globalconfig.InstrumentationInstallTime(),
		},
	}

	payloadLefts := make([]transport.Message, 0, len(payloads)/2)
	for _, payload := range payloads {
		switch payload.(type) {
		case transport.AppClientConfigurationChange:
			appStarted.Configuration = payload.(transport.AppClientConfigurationChange).Configuration
		case transport.AppProductChange:
			appStarted.Products = payload.(transport.AppProductChange).Products
		default:
			payloadLefts = append(payloadLefts, transport.Message{Payload: payload, RequestType: payload.RequestType()})
		}
	}

	// Following the documentation, an app-started payload cannot be put in a message-batch one.
	return []transport.Payload{
		appStarted,
		transport.MessageBatch{Payload: payloadLefts},
	}, MessageBatchTransformer
}

type messageBatchTransformer struct{}

var MessageBatchTransformer Transformer = &messageBatchTransformer{}

func (t *messageBatchTransformer) Transform(payloads []transport.Payload) ([]transport.Payload, Transformer) {
	messages := make([]transport.Message, len(payloads))
	for _, payload := range payloads {
		messages = append(messages, transport.Message{
			Payload:     payload,
			RequestType: payload.RequestType(),
		})
	}

	return []transport.Payload{transport.MessageBatch{Payload: messages}}, MessageBatchTransformer
}

type appClosingTransformer struct{}

var AppClosingTransformer Transformer = &appClosingTransformer{}

func (t *appClosingTransformer) Transform(payloads []transport.Payload) ([]transport.Payload, Transformer) {
	return MessageBatchTransformer.Transform(append(payloads, transport.AppClosing{}))
}
