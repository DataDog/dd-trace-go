// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mapper

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

type appStarted struct {
	wrapper
}

func NewAppStartedMapper(underlying Mapper) Mapper {
	return &appStarted{wrapper{underlying}}
}

func (t *appStarted) Transform(payloads []transport.Payload) ([]transport.Payload, Mapper) {
	appStarted := &transport.AppStarted{
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

	// Following the documentation, an app-started payload cannot be put in a message-batch one so we don't forward it to the next transformation,
	// and we also need to put the app-started payload at the beginning of the slice
	mappedPayloads, nextMapper := t.wrapper.Transform(payloadLefts)
	return append([]transport.Payload{appStarted}, mappedPayloads...), nextMapper
}
