// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

// ResetForTesting resets the global configuration state for testing.
//
// WARNING: This function is intended for use in tests only to reset state between
// test cases. It must not be called concurrently with Get() or other code that
// accesses the global config, as it can cause race conditions and violate the
// singleton initialization guarantee.
func ResetForTesting() {
	instance.Store(nil)
}
