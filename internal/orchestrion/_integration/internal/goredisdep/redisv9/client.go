// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package redisv9 creates a go-redis v9 client inside a dependency module.
package redisv9

import "github.com/redis/go-redis/v9"

// NewClient returns a client for addr.
func NewClient(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
}
