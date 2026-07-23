// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"flag"
	"sync"
	"testing"
)

func retryAttemptFailfastEnabled() bool {
	parsed := flag.Lookup("test.failfast")
	if parsed == nil {
		return false
	}
	getter, ok := parsed.Value.(flag.Getter)
	if !ok {
		return false
	}
	enabled, ok := getter.Get().(bool)
	return ok && enabled
}

// getRetryAttemptLayout returns the process-cached testing layout when every
// private section required by the fresh-attempt runtime is structurally valid.
// Compatibility is determined from the running binary's actual field names and
// types, not from its Go version or a source-file fingerprint.
func getRetryAttemptLayout() (*testingInternalsLayout, string) {
	return validateRetryAttemptLayout(getTestingInternalsLayout())
}

func validateRetryAttemptLayout(layout *testingInternalsLayout) (*testingInternalsLayout, string) {
	if layout == nil || layout.disabled || !layout.retryAttemptOK ||
		!layout.outputWriterOK || !layout.chattyOK || !layout.contextMatcherOK {
		return nil, "testing_t_layout_unsupported"
	}
	return layout, ""
}

// retryAttemptNativeFailureObserved reads the shared root failure latch through
// the runtime-discovered testing.T layout. Go does not expose numFailed or
// shouldFailFast, so this access uses the same cached offsets as the other
// testing internals helpers.
func retryAttemptNativeFailureObserved(t *testing.T) bool {
	layout, _ := getRetryAttemptLayout()
	if layout == nil || t == nil {
		return false
	}
	base := commonBaseForTest(t, layout)
	if base == nil {
		return false
	}
	for parent := pointerWord(base, layout.common.parent); parent != nil; parent = pointerWord(base, layout.common.parent) {
		base = parent
	}
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.RLock()
	failed := *fieldPtr[bool](base, layout.common.failed)
	mu.RUnlock()
	return failed
}
