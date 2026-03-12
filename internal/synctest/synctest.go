// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package synctest

import (
	"testing"
	"testing/synctest"
)

func Test(t *testing.T, f func(t *testing.T)) {
	synctest.Test(t, func(t *testing.T) {
		f(t)
	})
}

// Wait waits until all goroutines in the current synctest bubble are blocked.
// It is a re-export of the standard library's synctest.Wait.
func Wait() {
	synctest.Wait()
}
