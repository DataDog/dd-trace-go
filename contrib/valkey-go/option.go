// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package valkey

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

type clientConfig struct {
	rawCommand bool
}

// ClientOption represents an option that can be used to create or wrap a client.
type ClientOption func(*clientConfig)

func defaults(cfg *clientConfig) {
	// Unless Agent supports obfuscation for valkey, we should not enable raw command.
	cfg.rawCommand = internal.BoolEnv("DD_TRACE_VALKEY_RAW_COMMAND", false)
}

// WithRawCommand reports whether to keep the raw command value
// on instrumenation spans.
func WithRawCommand(rawCommand bool) ClientOption {
	return func(cfg *clientConfig) {
		cfg.rawCommand = rawCommand
	}
}
