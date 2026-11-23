// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build deadlock

package tracer

// tracerCleanStopIterations is the per-goroutine iteration count for
// TestTracerCleanStop. Each iteration starts the global tracer up to 3 times,
// and under the deadlock build tag every Start() allocates a locking.RWMutex
// per Span/spanContext/trace plus a locking.Mutex on the tracer itself; the
// deadlock detector's lock-order map keys on those mutexes' addresses, which
// are interior pointers that keep the whole tracer reachable until the map is
// pruned. At the full 5000 iterations (15,000 Start() calls) this measured
// 4.84GB of peak RSS, against 0.11GB on the same test without this tag. Scale
// down here rather than in the shared test body, so the concurrent
// start/stop/span-creation shape the test exists to exercise is unchanged --
// only its volume is.
const tracerCleanStopIterations = 1000
