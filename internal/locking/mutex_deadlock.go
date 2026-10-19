// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build deadlock

package locking

import deadlock "github.com/linkdata/deadlock"

// A Mutex is a mutual exclusion lock.
type Mutex struct {
	deadlock.Mutex
}

// An RWMutex is a reader/writer mutual exclusion lock.
type RWMutex struct {
	deadlock.RWMutex
}

func init() {
	deadlock.Opts.WriteLocked(func() {
		// Upstream's 64Ki default assumes a handful of long-lived, statically
		// allocated mutexes. dd-trace-go embeds Mutex/RWMutex by value in
		// per-object fields (Span, spanContext, trace, tracer), so the detector's
		// lock-order map keys on interior pointers into those objects, which keeps
		// the whole object reachable for as long as the entry survives. Nothing
		// prunes the map short of hitting the cap, so at the default a test that
		// starts/stops the tracer thousands of times retains all of them: measured
		// at 4.84GB with the default cap vs 0.11GB without the deadlock tag on
		// TestTracerCleanStop. Lower the cap so the detector retains fewer
		// pointers to short-lived objects. Upstream clears the entire order map
		// at the cap, so older lock-order evidence can be lost sooner.
		//
		// Do not set this to 0: preLock (lockorder.go) is skipped entirely when
		// MaxMapSize is 0, which disables both recursive-locking and
		// inconsistent-lock-order detection -- only the 30s wait-timeout detector
		// would remain.
		deadlock.Opts.MaxMapSize = 4096
	})
}
