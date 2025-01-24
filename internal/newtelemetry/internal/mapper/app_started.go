// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mapper

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

type appStartedReducer struct {
	wrapper
}

// NewAppStartedMapper returns a new Mapper that adds an AppStarted payload to the beginning of all payloads
// and pass it down to irs underlying mapper.
// The AppStarted payload ingest the [transport.AppClientConfigurationChange] and [transport.AppProductChange] payloads
func NewAppStartedMapper(underlying Mapper) Mapper {
	return &appStartedReducer{wrapper{underlying}}
}

func (t *appStartedReducer) Transform(payloads []transport.Payload) ([]transport.Payload, Mapper) {
	appStarted := transport.AppStarted{
		InstallSignature: transport.InstallSignature{
			InstallID:   globalconfig.InstrumentationInstallID(),
			InstallType: globalconfig.InstrumentationInstallType(),
			InstallTime: globalconfig.InstrumentationInstallTime(),
		},
	}

	payloadLefts := make([]transport.Payload, 0, len(payloads))
	for _, payload := range payloads {
		switch payload.(type) {
		case transport.AppClientConfigurationChange:
			appStarted.Configuration = payload.(transport.AppClientConfigurationChange).Configuration
		case transport.AppProductChange:
			appStarted.Products = payload.(transport.AppProductChange).Products
		default:
			payloadLefts = append(payloadLefts, payload)
		}
	}

	// The app-started event should be the first event in the payload and not in an message-batch
	payloads, mapper := t.wrapper.Transform(payloadLefts)
	return append([]transport.Payload{appStarted}, payloads...), mapper
}
