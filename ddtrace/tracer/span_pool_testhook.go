// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build deadlock

package tracer

// testAcquireSpan, when non-nil, supplies the next span returned by acquireSpan
// instead of the pool. It exists only for the lock-ordering deadlock test, which
// must drive two StartSpan calls back through a specific pair of *Span instances
// so the reversed lock ordering is recorded on the same mutexes. sync.Pool cannot
// do this deterministically (it is process-global, the worker concurrently Puts
// into it, and it guarantees no Get/Put ordering or identity). The hook is read
// only on the StartSpan path, which only the test goroutine drives while it is
// set, so it needs no synchronization.
var testAcquireSpan func() *Span

func maybeTestAcquireSpan() *Span {
	if testAcquireSpan != nil {
		return testAcquireSpan()
	}
	return nil
}
