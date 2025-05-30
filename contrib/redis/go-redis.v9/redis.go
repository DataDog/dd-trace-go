// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package redis provides functions to trace the redis/go-redis package (https://github.com/redis/go-redis).
package redis

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/redis/go-redis.v9/v2"

	"github.com/redis/go-redis/v9"
)

// NewClient returns a new Client that is traced with the default tracer under
// the service name "redis".
func NewClient(opt *redis.Options, opts ...ClientOption) redis.UniversalClient {
	return v2.NewClient(opt, opts...)
}

// WrapClient adds a hook to the given client that traces with the default tracer under
// the service name "redis".
func WrapClient(client redis.UniversalClient, opts ...ClientOption) {
	v2.WrapClient(client, opts...)
}
