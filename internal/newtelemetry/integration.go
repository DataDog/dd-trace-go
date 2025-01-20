// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

type integrations struct {
	mu           sync.Mutex
	integrations []Integration
}

func (i *integrations) Add(integration Integration) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.integrations = append(i.integrations, integration)
}

func (i *integrations) Payload() transport.Payload {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.integrations) == 0 {
		return nil
	}

	integrations := make([]transport.Integration, len(i.integrations))
	for idx, integration := range i.integrations {
		integrations[idx] = transport.Integration{
			Name:    integration.Name,
			Version: integration.Version,
			Enabled: true,
			Error:   integration.Error,
		}
	}
	i.integrations = nil
	return transport.AppIntegrationChange{
		Integrations: integrations,
	}
}
