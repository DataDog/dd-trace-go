// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtrackerstate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// reset restores the zero state around a test.
func reset(t *testing.T) {
	t.Helper()
	zero := func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.resolve, state.pending, state.deferred = nil, false, nil
	}
	zero()
	t.Cleanup(zero)
	t.Cleanup(IgnoreTestBinaryForTesting())
}

// A test binary of package main never runs main, so nothing would release the
// deferred starts. The flag must stay false there.
func TestMarkSupervisorPendingInTestBinary(t *testing.T) {
	reset(t)
	inTestBinary = testing.Testing // the real check: this is a test binary
	SetResolver(func() bool { return true })
	assert.False(t, MarkSupervisorPending())
	assert.False(t, SupervisorPending())
}

func TestMarkSupervisorPending(t *testing.T) {
	tests := []struct {
		name    string
		resolve func() bool
		want    bool
	}{
		{name: "no resolver registered", resolve: nil, want: false},
		{name: "process becomes the supervisor", resolve: func() bool { return true }, want: true},
		{name: "process stays the application", resolve: func() bool { return false }, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			assert.False(t, SupervisorPending(), "the flag is false until the aspect marks it")
			SetResolver(tt.resolve)
			assert.False(t, SupervisorPending(), "registering a resolver does not set the flag")
			assert.Equal(t, tt.want, MarkSupervisorPending())
			assert.Equal(t, tt.want, SupervisorPending())
		})
	}
}

func TestRelease(t *testing.T) {
	t.Run("runs deferred functions once and clears the flag", func(t *testing.T) {
		reset(t)
		SetResolver(func() bool { return true })
		assert.True(t, MarkSupervisorPending())
		var order []string
		Defer(func() {
			assert.False(t, SupervisorPending(), "the flag is cleared before a deferred function runs")
			order = append(order, "first")
		})
		Defer(func() { order = append(order, "second") })

		Release()
		Release()

		assert.Equal(t, []string{"first", "second"}, order)
		assert.False(t, SupervisorPending())
	})

	t.Run("without the aspect it is a no-op", func(t *testing.T) {
		reset(t)
		assert.NotPanics(t, Release)
		assert.False(t, SupervisorPending())
	})
}
