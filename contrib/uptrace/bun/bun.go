// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// Package bun provides helper functions for tracing the github.com/uptrace/bun package (https://github.com/uptrace/bun).
package bun

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/uptrace/bun/v2"

	"github.com/uptrace/bun"
)

// Wrap augments the given DB with tracing.
func Wrap(db *bun.DB, opts ...Option) {
	v2.Wrap(db, opts...)
}
