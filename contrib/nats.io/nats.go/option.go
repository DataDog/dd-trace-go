// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package nats

const (
	serviceName   = "nats"
	operationName = "nats.query"
)

type wrapConfig struct {
	serviceName string
}

func newWrapConfig() wrapConfig {
	return wrapConfig{
		serviceName: serviceName,
	}
}

// WrapOption represents an option that can be used to create or wrap a client.
type WrapOption func(*wrapConfig)

// WithServiceName sets the given service name for the wrapped resource.
func WithServiceName(name string) WrapOption {
	return func(cfg *wrapConfig) {
		cfg.serviceName = name
	}
}
