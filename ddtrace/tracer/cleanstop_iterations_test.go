// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build !deadlock

package tracer

// tracerCleanStopIterations is the per-goroutine iteration count for
// TestTracerCleanStop. See cleanstop_iterations_deadlock_test.go for why this
// is lower under the deadlock build tag.
const tracerCleanStopIterations = 5000
