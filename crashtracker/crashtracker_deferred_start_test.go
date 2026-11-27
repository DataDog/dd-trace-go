// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/crashtrackerstate"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"
	"github.com/DataDog/dd-trace-go/v2/profiler"
)

func TestSupervisorWillStart(t *testing.T) {
	parent := strconv.Itoa(os.Getppid())
	stray := strconv.Itoa(os.Getppid() + 1)
	exe, err := os.Executable()
	require.NoError(t, err)
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	tests := []struct {
		name    string
		enabled string
		marker  string
		active  string
		want    bool
	}{
		{name: "enabled by default, no marker", want: true},
		{name: "enabled explicitly, no marker", enabled: "true", want: true},
		{name: "disabled, no marker", enabled: "false", want: false},
		{name: "enabled, marker names the parent", marker: parent + ":7", want: false},
		{name: "disabled, marker names the parent", enabled: "false", marker: parent + ":7", want: false},
		{name: "enabled, stray marker", marker: stray + ":7", want: true},
		{name: "active marker matches parent and executable", active: parent + ":" + exe, want: runtime.GOOS == "windows"},
		{name: "active marker names a different parent", active: stray + ":" + exe, want: true},
		{name: "active marker names a different executable", active: parent + ":" + exe + ".other", want: true},
		{name: "active marker has an invalid PID", active: "invalid:" + exe, want: true},
		{name: "active marker has no executable", active: parent, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DD_CRASHTRACKING_ENABLED", tt.enabled)
			if tt.enabled == "" {
				require.NoError(t, os.Unsetenv("DD_CRASHTRACKING_ENABLED"))
			}
			t.Setenv(supervisedFDEnv, tt.marker)
			if tt.marker == "" {
				require.NoError(t, os.Unsetenv(supervisedFDEnv))
			}
			t.Setenv(crashTrackingActiveEnv, tt.active)
			if tt.active == "" {
				require.NoError(t, os.Unsetenv(crashTrackingActiveEnv))
			}
			assert.Equal(t, tt.want, supervisorWillStart())
		})
	}
}

// startTracerAndProfiler calls tracer.Start and profiler.Start the way the
// Orchestrion init functions of package main do, against an unreachable agent.
func startTracerAndProfiler(t *testing.T) {
	t.Helper()
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
	t.Setenv("DD_TRACE_STARTUP_LOGS", "false")
	t.Cleanup(func() {
		// Release first: it starts any deferred tracer and profiler, which Stop then stops.
		crashtrackerstate.Release()
		tracer.Stop()
		profiler.Stop()
		globalconfig.SetServiceName("")
	})
	require.NoError(t, tracer.Start(tracer.WithService("deferred-start-test"), tracer.WithAgentAddr("127.0.0.1:1")))
	require.NoError(t, profiler.Start(
		profiler.WithService("deferred-start-test"),
		profiler.WithAgentAddr("127.0.0.1:1"),
		profiler.WithPeriod(time.Hour),
		profiler.WithProfileTypes(profiler.HeapProfile),
	))
}

func tracerStarted() bool   { return globalconfig.ServiceName() != "" }
func profilerStarted() bool { return traceprof.ProfilerEnabled() }

// markPendingLikeAspect runs the package variable initializer that the
// Orchestrion aspect injects into package main.
func markPendingLikeAspect() bool {
	restore := crashtrackerstate.IgnoreTestBinaryForTesting()
	defer restore()
	return crashtrackerstate.MarkSupervisorPending()
}

// The supervisor never returns from Start. Goexit models that: it ends the
// goroutine without running the code after start.
func TestSupervisorDoesNotStartTracerOrProfiler(t *testing.T) {
	t.Setenv("DD_CRASHTRACKING_ENABLED", "true")
	require.NoError(t, os.Unsetenv(supervisedFDEnv))
	previous := superviseApplication
	t.Cleanup(func() { superviseApplication = previous })
	superviseApplication = func(*config) error {
		runtime.Goexit()
		return nil
	}

	require.True(t, markPendingLikeAspect())
	startTracerAndProfiler(t) // the injected init functions
	assert.False(t, tracerStarted(), "the tracer started in the supervisor")
	assert.False(t, profilerStarted(), "the profiler started in the supervisor")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = startAndRelease()
	}()
	<-done

	assert.False(t, tracerStarted(), "the tracer started in the supervisor")
	assert.False(t, profilerStarted(), "the profiler started in the supervisor")
	assert.True(t, crashtrackerstate.SupervisorPending())
}

func TestSupervisionFailureStartsTracerAndProfiler(t *testing.T) {
	t.Setenv("DD_CRASHTRACKING_ENABLED", "true")
	require.NoError(t, os.Unsetenv(supervisedFDEnv))
	errSupervise := errors.New("supervision failed")
	previous := superviseApplication
	t.Cleanup(func() { superviseApplication = previous })
	superviseApplication = func(*config) error {
		assert.False(t, tracerStarted(), "the tracer started before the supervision failed")
		assert.False(t, profilerStarted(), "the profiler started before the supervision failed")
		return errSupervise
	}

	require.True(t, markPendingLikeAspect())
	startTracerAndProfiler(t) // the injected init functions
	require.False(t, tracerStarted())
	require.False(t, profilerStarted())

	err := startAndRelease() // the injected first statement of main
	require.ErrorIs(t, err, errSupervise)

	assert.True(t, tracerStarted(), "the tracer did not start after the supervision failed")
	assert.True(t, profilerStarted(), "the profiler did not start after the supervision failed")
	assert.False(t, crashtrackerstate.SupervisorPending())
}

func TestSkippedSupervisorReleasesDeferredTracerAndProfiler(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not skip supervision in subprocesses")
	}
	t.Setenv("DD_CRASHTRACKING_ENABLED", "true")
	require.NoError(t, os.Unsetenv(supervisedFDEnv))
	previous := superviseApplication
	t.Cleanup(func() { superviseApplication = previous })
	superviseApplication = func(*config) error {
		return errors.New("unexpected supervision in an application subprocess")
	}

	require.True(t, markPendingLikeAspect())
	startTracerAndProfiler(t)
	require.False(t, tracerStarted())
	require.False(t, profilerStarted())

	exe, err := currentExecutable()
	require.NoError(t, err)
	t.Setenv(crashTrackingActiveEnv, strconv.Itoa(os.Getppid())+":"+exe)
	assert.False(t, supervisorWillStart())
	require.NoError(t, startAndRelease())
	assert.True(t, tracerStarted())
	assert.True(t, profilerStarted())
	assert.False(t, crashtrackerstate.SupervisorPending())
}

func TestApplicationProcessStartsTracerAndProfiler(t *testing.T) {
	t.Setenv("DD_CRASHTRACKING_ENABLED", "true")
	t.Setenv(supervisedFDEnv, strconv.Itoa(os.Getppid())+":7")

	assert.False(t, markPendingLikeAspect(), "the flag is set in the application process")
	startTracerAndProfiler(t) // the injected init functions

	assert.True(t, tracerStarted(), "the tracer did not start in the application process")
	assert.True(t, profilerStarted(), "the profiler did not start in the application process")
}
