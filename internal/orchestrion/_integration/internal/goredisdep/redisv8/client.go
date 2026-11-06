// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package redisv8 creates a go-redis v8 client inside a dependency module.
package redisv8

import "github.com/go-redis/redis/v8"

// NewClient returns a client for addr.
func NewClient(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: addr})
}
