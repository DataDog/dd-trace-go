// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package valkey

import (
	"github.com/valkey-io/valkey-go"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

type config struct {
	rawCommand  bool
	serviceName string
	errCheck    func(err error) bool
}

// Option represents an option that can be used to create or wrap a client.
type Option func(*config)

func defaultConfig() *config {
	return &config{
		// Do not include the raw command by default since it could contain sensitive data.
		rawCommand:  internal.BoolEnv("DD_TRACE_VALKEY_RAW_COMMAND", false),
		serviceName: namingschema.ServiceName(defaultServiceName),
		errCheck: func(err error) bool {
			return err != nil && !valkey.IsValkeyNil(err)
		},
	}
}

// WithRawCommand can be used to set a tag `valkey.raw_command` in the created spans (disabled by default).
// Warning: please note the datadog-agent v7.63.x or below does not support obfuscation for this tag, so use this at your own risk.
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

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error.
func WithErrorCheck(fn func(err error) bool) Option {
	return func(cfg *config) {
		cfg.errCheck = fn
	}
}
