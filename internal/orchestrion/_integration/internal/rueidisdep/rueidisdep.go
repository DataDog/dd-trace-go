// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package rueidisdep creates a rueidis client inside a dependency module.
package rueidisdep

import "github.com/redis/rueidis"

// NewClient returns a client for addr.
func NewClient(addr string) (rueidis.Client, error) {
	return rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{addr}})
}
