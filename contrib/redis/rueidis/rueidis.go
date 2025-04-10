// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

// Package rueidis provides tracing functions for tracing the redis/rueidis package (https://github.com/redis/rueidis).
package rueidis

import (
	"github.com/redis/rueidis"

	v2 "github.com/DataDog/dd-trace-go/contrib/redis/rueidis/v2"
)

// NewClient returns a new rueidis.Client enhanced with tracing.
func NewClient(clientOption rueidis.ClientOption, opts ...Option) (rueidis.Client, error) {
	return v2.NewClient(clientOption, opts...)
}
