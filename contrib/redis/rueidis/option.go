// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

package rueidis

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/redis/rueidis/v2"
)

// Option represents an option that can be used to create or wrap a client.
type Option = v2.Option

// WithRawCommand can be used to set a tag `redis.raw_command` in the created spans (disabled by default).
func WithRawCommand(rawCommand bool) Option {
	return v2.WithRawCommand(rawCommand)
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) Option {
	return v2.WithService(name)
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error.
func WithErrorCheck(fn func(err error) bool) Option {
	return v2.WithErrorCheck(fn)
}
