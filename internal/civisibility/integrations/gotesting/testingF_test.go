// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompleteFuzzTargetLifecycleObservesCleanupPanic(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(func() { panic("cleanup panic") })

	terminal, _ := completeFuzzTargetLifecycle(f, true, nil)

	require.Equal(t, "cleanup panic", terminal)
	require.True(t, f.Failed())
}

func TestCompleteFuzzTargetLifecyclePreservesBodyPanicOverCleanupPanic(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(func() { panic("cleanup panic") })

	terminal, _ := completeFuzzTargetLifecycle(f, false, "body panic")

	require.Equal(t, "body panic", terminal)
	require.True(t, f.Failed())
}

func TestCompleteFuzzTargetLifecycleObservesCleanupFailure(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(f.Fail)

	terminal, _ := completeFuzzTargetLifecycle(f, true, nil)

	require.Nil(t, terminal)
	require.True(t, f.Failed())
}

func TestCompleteFuzzTargetLifecycleObservesMissingFuzzCall(t *testing.T) {
	f := &testing.F{}

	terminal, _ := completeFuzzTargetLifecycle(f, true, nil)

	require.Nil(t, terminal)
	require.True(t, f.Failed())
}

func TestCompleteFuzzTargetLifecyclePreservesCleanupSkip(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(func() { f.Skip("cleanup skip") })

	terminal, _ := completeFuzzTargetLifecycle(f, true, nil)

	require.Nil(t, terminal)
	require.True(t, f.Skipped())
	require.False(t, f.Failed())
}

func TestCompleteFuzzTargetLifecycleRejectsCleanupGoexit(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(runtime.Goexit)

	terminal, _ := completeFuzzTargetLifecycle(f, true, nil)

	require.ErrorIs(t, terminal.(error), errTestingDidNotReturn)
	require.True(t, f.Failed())
}
