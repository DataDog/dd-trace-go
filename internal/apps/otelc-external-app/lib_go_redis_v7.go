// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import "github.com/go-redis/redis/v7"

func init() {
	registerLibrary("go_redis_v7", func() {
		_ = redis.NewClient(&redis.Options{}).Close()
	})
}
