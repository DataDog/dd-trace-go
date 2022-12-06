//go:build go1.19

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package atomic

import "sync/atomic"

// Int64 is an alias for atomic.Int64.
type Int64 = atomic.Int64

// Value is an alias for atomic.Value.
type Value = atomic.Value
