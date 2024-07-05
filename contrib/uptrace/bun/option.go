// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package bun

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type config struct {
	serviceName string
}

// Option represents an option that can be used to create or wrap a client.
type Option func(*config)

func defaults(cfg *config) {
	service := defaultServiceName
	if svc := globalconfig.ServiceName(); svc != "" {
		service = svc
	}
	cfg.serviceName = service
}

// WithService sets the given service name for the client.
func WithService(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}
