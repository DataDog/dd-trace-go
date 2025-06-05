// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package redis provides tracing functions for tracing the go-redis/redis package (https://github.com/go-redis/redis).
// This package supports versions up to go-redis 6.15.
package redis

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2"

	"github.com/go-redis/redis"
)

// Client is used to trace requests to a redis server.
type Client = v2.Client

var _ redis.Cmdable = (*Client)(nil)

// Pipeliner is used to trace pipelines executed on a Redis server.
type Pipeliner = v2.Pipeliner

var _ redis.Pipeliner = (*Pipeliner)(nil)

// NewClient returns a new Client that is traced with the default tracer under
// the service name "redis".
func NewClient(opt *redis.Options, opts ...ClientOption) *Client {
	return v2.NewClient(opt, opts...)
}

// WrapClient wraps a given redis.Client with a tracer under the given service name.
func WrapClient(c *redis.Client, opts ...ClientOption) *Client {
	return v2.WrapClient(c, opts...)
}
