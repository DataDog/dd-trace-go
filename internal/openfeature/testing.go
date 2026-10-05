// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import "github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"

// ResetForTest resets the global rcState for test isolation.
func ResetForTest() {
	rcState.Lock()
	defer rcState.Unlock()
	rcState.subscribed = false
	rcState.callback = nil
	rcState.buffered = nil
}

// SetSubscribedForTest sets the subscribed flag without actually calling Subscribe.
func SetSubscribedForTest(v bool) {
	rcState.Lock()
	defer rcState.Unlock()
	rcState.subscribed = v
}

// SetBufferedForTest sets a buffered update for testing.
func SetBufferedForTest(u remoteconfig.ProductUpdate) {
	rcState.Lock()
	defer rcState.Unlock()
	rcState.buffered = u
}

// GetBufferedForTest returns the current buffered update for testing.
func GetBufferedForTest() remoteconfig.ProductUpdate {
	rcState.Lock()
	defer rcState.Unlock()
	return rcState.buffered
}
