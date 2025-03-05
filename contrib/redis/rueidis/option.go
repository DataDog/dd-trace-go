// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package rueidis

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

type config struct {
	rawCommand  bool
	serviceName string
}

// Option represents an option that can be used to create or wrap a client.
type Option func(*config)

func defaultConfig() *config {
	return &config{
		// Do not include the raw command by default since it could contain sensitive data.
		rawCommand:  internal.BoolEnv("DD_TRACE_REDIS_RAW_COMMAND", false),
		serviceName: namingschema.ServiceName(defaultServiceName),
	}
}

// WithRawCommand can be used to set a tag `redis.raw_command` in the created spans (disabled by default).
func WithRawCommand(rawCommand bool) Option {
	return func(cfg *config) {
		cfg.rawCommand = rawCommand
	}
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}
