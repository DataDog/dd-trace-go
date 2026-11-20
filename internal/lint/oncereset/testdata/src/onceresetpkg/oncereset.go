// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onceresetpkg

import "sync"

var (
	once    sync.Once
	oncePtr *sync.Once
)

func TestFoo() {
	once = sync.Once{}     // want "reset sync.Once outside reset\\*ForTesting or mark\\*ForTesting"
	*oncePtr = sync.Once{} // want "reset sync.Once outside reset\\*ForTesting or mark\\*ForTesting"
}

func TestCleanup() {
	reset := func() {
		once = sync.Once{} // want "reset sync.Once outside reset\\*ForTesting or mark\\*ForTesting"
	}
	reset()
}

func resetFooForTesting() {
	once = sync.Once{}
	reset := func() {
		once = sync.Once{}
	}
	reset()
}

func markFooForTesting() {
	once = sync.Once{}
}

func ResetForTesting() {
	once = sync.Once{}
}

func newLocalOnce() {
	tmp := sync.Once{}
	_ = tmp
	oncePtr = &sync.Once{}
}

func productionShutdown() {
	once = sync.Once{} //nolint:oncereset
}
