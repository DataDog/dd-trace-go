// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build deadlock

package tracer

// deadlockBuildEnabled reports that the deadlock detector build tag is active.
// Under this tag, github.com/linkdata/deadlock retains interior mutex pointers
// in a global lock-order map until MaxMapSize is reached, which keeps locked
// Span objects alive across GC passes.
const deadlockBuildEnabled = true
