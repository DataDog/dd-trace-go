// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build !deadlock

package tracer

// maybeTestAcquireSpan is a no-op in normal builds. It is inlined and the dead
// branch in acquireSpan is eliminated, so it costs nothing on the hot path.
func maybeTestAcquireSpan() *Span { return nil }
