// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package redisv7 creates a go-redis v7 client inside a dependency module.
package redisv7

import "github.com/go-redis/redis/v7"

// NewClient returns a client for addr.
func NewClient(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: addr})
}
