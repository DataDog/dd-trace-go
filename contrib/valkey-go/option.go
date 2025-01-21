// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package valkey

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

type clientConfig struct {
	skipRaw bool
}

// ClientOption represents an option that can be used to create or wrap a client.
type ClientOption func(*clientConfig)

func defaults(cfg *clientConfig) {
	cfg.skipRaw = internal.BoolEnv("DD_TRACE_VALKEY_SKIP_RAW_COMMAND", false)
}

// WithSkipRawCommand reports whether to skip setting the raw command value
// on instrumenation spans. This may be useful if the Datadog Agent is not
// set up to obfuscate this value and it could contain sensitive information.
func WithSkipRawCommand(skip bool) ClientOption {
	return func(cfg *clientConfig) {
		cfg.skipRaw = skip
	}
}
