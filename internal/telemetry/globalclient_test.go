// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

// Ensure that DD_INSTRUMENTATION_TELEMETRY_ENABLED is read once and cached,
// matching the expectation that env vars are set before telemetry is first used.
func TestDisabledCachesInitialEnv(t *testing.T) {
	// Reset lazy init state
	telemetryEnabledOnce = sync.Once{}

	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "0")
	require.True(t, Disabled())

	// Changing the env after the first call should not flip the cached value.
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	require.True(t, Disabled())

	// Reset again
	telemetryEnabledOnce = sync.Once{}
}

// TestLog_QueuedBeforeStartApp_CapturesCallSiteStacktrace reproduces the bug
// WithStacktrace() actually captures nothing itself — the real capture used
// to happen inside loggerBackend.add, at replay time for a call queued
// before StartApp. That attributed the stack to the replay goroutine
// (SwapClient/globalClientRecorder.Replay), not to this test function, the
// call's real site. Log now captures synchronously before queuing, and this
// asserts the rendered stack reflects that: no replay-machinery frames
// (SwapClient/Replay/globalClientCall), just this test function.
func TestLog_QueuedBeforeStartApp_CapturesCallSiteStacktrace(t *testing.T) {
	telemetryEnabledOnce = sync.Once{}
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })

	globalClientRecorder.Clear()
	require.Nil(t, GlobalClient(), "no client must be installed yet for this test to exercise the queued path")

	// The exact bug scenario: call the package-level Log function before any
	// client exists, so globalClientCall queues the closure instead of
	// running it immediately.
	Log(NewRecord(LogError, "queued before start"), WithStacktrace())

	tracerConfig := internal.TracerConfig{Service: "test-service", Env: "test-env", Version: "1.0.0"}
	config := defaultConfig(ClientConfig{})
	config.AgentURL = "http://localhost:8126"
	config.FlushInterval = internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour}
	c, err := newClient(tracerConfig, config)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	t.Cleanup(func() { SwapClient(nil) })

	recordWriter := &internal.RecordWriter{}
	c.writer = recordWriter

	// Installing the client replays the queued Log call — on this goroutine,
	// not the original call site's.
	old := SwapClient(c)
	require.Nil(t, old)

	c.Flush()

	payloads := recordWriter.Payloads()
	require.NotEmpty(t, payloads)
	// Flushes with more than one payload (e.g. a heartbeat alongside the
	// replayed logs) are wrapped in a transport.MessageBatch by the default
	// mapper, so unwrap that layer when present.
	var logs transport.Logs
	for _, p := range payloads {
		switch v := p.(type) {
		case transport.Logs:
			logs = v
		case transport.MessageBatch:
			for _, m := range v {
				if l, ok := m.Payload.(transport.Logs); ok {
					logs = l
				}
			}
		}
	}
	require.Len(t, logs.Logs, 1)

	stack := logs.Logs[0].StackTrace
	assert.Contains(t, stack, "TestLog_QueuedBeforeStartApp_CapturesCallSiteStacktrace")
	assert.NotContains(t, stack, "SwapClient")
	assert.NotContains(t, stack, "Replay")
	assert.NotContains(t, stack, "globalClientCall")
}

// TestLog_DisabledSkipsStacktraceCapture guards against a regression: Log
// used to capture a stacktrace whenever the caller requested one and no
// client was installed yet — even when telemetry was fully disabled, in
// which case globalClientCall discards the call a line later via its own
// Disabled() check anyway. When telemetry is disabled, StartApp never
// installs a client, so GlobalClient() is permanently nil: a hot,
// repeatedly-invoked disabled path (e.g. AppSec's exception recording)
// would pay for a stack walk and allocation it can never observe.
//
// This compares allocations for the same Log(record, WithStacktrace()) call
// under two states — disabled vs. enabled-but-not-yet-started — holding the
// call-site argument construction (building the one-element options slice,
// the WithStacktrace() closure) constant across both. Only the internal
// capture branch should differ, so the disabled case must allocate less.
func TestLog_DisabledSkipsStacktraceCapture(t *testing.T) {
	record := NewRecord(LogError, "should be a no-op when disabled")

	measure := func(disabled bool) float64 {
		telemetryEnabledOnce = sync.Once{}
		t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })
		if disabled {
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "0")
		} else {
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
		}

		globalClientRecorder.Clear()
		t.Cleanup(func() { globalClientRecorder.Clear() })
		require.Nil(t, GlobalClient())

		return testing.AllocsPerRun(200, func() {
			Log(record, WithStacktrace())
		})
	}

	disabledAllocs := measure(true)
	enabledNotStartedAllocs := measure(false)

	assert.Less(t, disabledAllocs, enabledNotStartedAllocs,
		"a disabled Log(..., WithStacktrace()) call must do less work than an enabled-but-not-started one — it must not capture a stacktrace it will immediately discard")
}

// TestAppStartedFlushPanicStopsClientWithoutDeadlock verifies that a panic in
// the asynchronous app-started flush disables the client without deadlocking.
// The panic recovery must not close the client from the flush goroutine
// itself: Close joins the app-started flush goroutine, so that close would
// wait for the very goroutine that runs it.
func TestAppStartedFlushPanicStopsClientWithoutDeadlock(t *testing.T) {
	// Force telemetry enabled: StartApp ignores calls while Disabled.
	telemetryEnabledOnce = sync.Once{}
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })
	t.Cleanup(func() { SwapClient(nil) })
	SwapClient(nil)

	tracerConfig := internal.TracerConfig{Service: "test-service", Env: "test-env", Version: "1.0.0"}
	config := defaultConfig(ClientConfig{})
	config.AgentURL = "http://localhost:8126"
	config.FlushInterval = internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour}
	c, err := newClient(tracerConfig, config)
	require.NoError(t, err)

	var panicked atomic.Bool
	c.AddFlushTicker(func(Client) {
		if !panicked.CompareAndSwap(false, true) {
			return
		}
		panic("boom: stop the app-started flush")
	})

	StartApp(c)

	// The app-started flush runs the ticker callbacks and panics.
	require.Eventually(t, panicked.Load, 5*time.Second, 10*time.Millisecond,
		"the app-started flush never ran the ticker callbacks")
	// The panic recovery removes the client from the global slot.
	require.Eventually(t, func() bool { return GlobalClient() == nil }, 5*time.Second, 10*time.Millisecond,
		"the panic recovery did not remove the global client")

	// The recovery closes the client on another goroutine once the flush
	// returns. Wait for that close to finish before closing here, so this
	// close observes the settled state rather than racing the recovery.
	time.Sleep(250 * time.Millisecond)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		c.Close()
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return: the panic recovery joined its own app-started flush goroutine")
	}
}

// TestCloseBoundsWaitOnAppStartedFlush verifies that Close does not wait
// forever for an app-started flush whose request the agent never answers.
// The mock agent below accepts requests and never responds, and the client
// has no request timeout, so only the bound in Close can return.
func TestCloseBoundsWaitOnAppStartedFlush(t *testing.T) {
	// Force telemetry enabled: StartApp ignores calls while Disabled.
	telemetryEnabledOnce = sync.Once{}
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })
	t.Cleanup(func() { SwapClient(nil) })
	SwapClient(nil)

	var requestSeen sync.Once
	requested := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestSeen.Do(func() { close(requested) })
		<-release // Hold every request: the agent never answers.
	}))
	defer srv.Close()
	defer close(release)

	tracerConfig := internal.TracerConfig{Service: "test-service", Env: "test-env", Version: "1.0.0"}
	config := defaultConfig(ClientConfig{
		AgentURL:   srv.URL,
		HTTPClient: &http.Client{}, // No timeout: the flush cannot bound itself.
	})
	config.FlushInterval = internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour}
	c, err := newClient(tracerConfig, config)
	require.NoError(t, err)

	StartApp(c)

	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("no request reached the mock agent")
	}

	// Close allows the app-started flush two seconds to deliver, then cancels
	// its request. The cancel should unwind this flush without using the second
	// two-second bound.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		c.Close()
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return: it waits without bound on the app-started flush")
	}

	// The cancel in Close unwinds the flush: the goroutine exits although the
	// agent still holds the request, so Close leaves no waiter behind. The
	// client clears its pending channel when the flush returns.
	require.Eventually(t, func() bool {
		c.startFlushMu.Lock()
		defer c.startFlushMu.Unlock()
		return c.startFlushDone == nil
	}, 5*time.Second, 10*time.Millisecond, "the app-started flush goroutine did not exit after the cancel")
}

// TestCloseFromFlushTickerCallbackReturns verifies that Close called from a
// flush ticker callback returns instead of waiting for the ticker worker:
// that worker runs the callback, so a joining stop would wait for itself and
// block forever. Close returns after the two-second delivery grace and the
// second two-second wait expire, which lets the callback return and the worker
// exit.
func TestCloseFromFlushTickerCallbackReturns(t *testing.T) {
	// Force telemetry enabled: StartApp ignores calls while Disabled.
	telemetryEnabledOnce = sync.Once{}
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })
	t.Cleanup(func() { SwapClient(nil) })
	SwapClient(nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tracerConfig := internal.TracerConfig{Service: "test-service", Env: "test-env", Version: "1.0.0"}
	config := defaultConfig(ClientConfig{
		AgentURL:   srv.URL,
		HTTPClient: &http.Client{Timeout: 100 * time.Millisecond},
	})
	config.FlushInterval = internal.Range[time.Duration]{Min: 10 * time.Millisecond, Max: 20 * time.Millisecond}
	c, err := newClient(tracerConfig, config)
	require.NoError(t, err)

	var closing sync.Once
	closedFromCallback := make(chan struct{})
	c.AddFlushTicker(func(cl Client) {
		closing.Do(func() {
			cl.Close()
			close(closedFromCallback)
		})
	})

	select {
	case <-closedFromCallback:
	case <-time.After(10 * time.Second):
		t.Fatal("Close called from a flush ticker callback did not return")
	}

	// The worker exits once the callback returned.
	require.Eventually(t, func() bool {
		select {
		case <-c.flushTicker.Done():
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond, "the ticker worker did not exit")
}

// TestInstallClientIfEmptyCompletesDistinctLoserMarker covers the concrete
// client marker lifecycle directly. Both clients prepare a startup marker,
// but only the CAS winner keeps its marker; the distinct loser's marker is
// completed without stopping or otherwise changing the winner.
func TestInstallClientIfEmptyCompletesDistinctLoserMarker(t *testing.T) {
	telemetryEnabledOnce = sync.Once{}
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })
	SwapClient(nil)

	newConcreteClient := func() *client {
		tracerConfig := internal.TracerConfig{Service: "test-service", Env: "test-env", Version: "1.0.0"}
		config := defaultConfig(ClientConfig{})
		config.AgentURL = "http://localhost:8126"
		config.FlushInterval = internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour}
		c, err := newClient(tracerConfig, config)
		require.NoError(t, err)
		c.writer = &internal.RecordWriter{}
		return c
	}

	winner := newConcreteClient()
	loser := newConcreteClient()
	winnerAttempt, empty := prepareStartApp()
	require.True(t, empty)
	defer winnerAttempt.done()
	loserAttempt, empty := prepareStartApp()
	require.True(t, empty)
	defer loserAttempt.done()
	winnerDone := winner.markStartFlushPending()
	loserDone := loser.markStartFlushPending()
	require.NotNil(t, winnerDone)
	require.NotNil(t, loserDone)
	t.Cleanup(func() {
		winner.completeStartFlush(winnerDone)
		loser.completeStartFlush(loserDone)
		SwapClient(nil)
		_ = winner.Close()
		_ = loser.Close()
	})

	installed := installClientIfEmpty(winner, winner, winnerDone, winnerAttempt.generation)
	require.True(t, installed)

	installed = installClientIfEmpty(loser, loser, loserDone, loserAttempt.generation)
	require.False(t, installed)
	completeLosingStartFlush(loser, loserDone)

	loser.startFlushMu.Lock()
	loserPending := loser.startFlushDone
	loserReservations := loser.startFlushReservations
	loserClaimed := loser.startFlushClaimed
	loser.startFlushMu.Unlock()
	assert.Nil(t, loserPending, "losing distinct client retained an unused startup marker")
	assert.Zero(t, loserReservations, "losing distinct client retained a startup marker reservation")
	assert.False(t, loserClaimed, "losing distinct client retained a claimed startup marker")
	select {
	case <-loserDone:
	default:
		t.Fatal("losing distinct client's unused startup marker was not completed")
	}

	winner.startFlushMu.Lock()
	winnerPending := winner.startFlushDone
	winnerReservations := winner.startFlushReservations
	winnerClaimed := winner.startFlushClaimed
	winner.startFlushMu.Unlock()
	assert.Equal(t, winnerDone, winnerPending, "loser cleanup changed the winner's startup marker")
	assert.Zero(t, winnerReservations, "winner retained a startup marker reservation")
	assert.True(t, winnerClaimed, "winner's startup marker was not claimed")
	select {
	case <-winnerDone:
		t.Fatal("loser cleanup completed the winner's startup marker")
	default:
	}
	select {
	case <-winner.flushTicker.Done():
		t.Fatal("loser cleanup stopped the winner's ticker")
	default:
	}
	require.Same(t, winner, GlobalClient())
}

// TestShutdownInvalidatesPreparedStart verifies that a shutdown which observes
// an empty global slot still invalidates an older prepared start. The test
// pauses at the same phase boundary StartApp uses: after AppStart, the
// WaitGroup increment, and marker creation, but before installation.
func TestShutdownInvalidatesPreparedStart(t *testing.T) {
	for _, tc := range []struct {
		name     string
		shutdown func()
	}{
		{name: "StopApp", shutdown: StopApp},
		{name: "SwapClientNil", shutdown: func() { SwapClient(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			telemetryEnabledOnce = sync.Once{}
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
			t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })
			SwapClient(nil)

			newConcreteClient := func() (*client, *internal.RecordWriter) {
				tracerConfig := internal.TracerConfig{Service: "test-service", Env: "test-env", Version: "1.0.0"}
				config := defaultConfig(ClientConfig{})
				config.AgentURL = "http://localhost:8126"
				config.FlushInterval = internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour}
				c, err := newClient(tracerConfig, config)
				require.NoError(t, err)
				writer := &internal.RecordWriter{}
				c.writer = writer
				return c, writer
			}

			stale, staleWriter := newConcreteClient()
			var fresh *client
			t.Cleanup(func() {
				SwapClient(nil)
				_ = stale.Close()
				if fresh != nil {
					_ = fresh.Close()
				}
			})

			attempt, empty := prepareStartApp()
			require.True(t, empty)
			stale.AppStart()
			staleDone := stale.markStartFlushPending()
			require.NotNil(t, staleDone)

			tc.shutdown()
			newAttempt, empty := prepareStartApp()
			require.True(t, empty)
			newAttempt.done()
			require.NotEqual(t, attempt.generation, newAttempt.generation, "shutdown did not advance the lifecycle generation")
			require.False(t, finishStartApp(stale, stale, staleDone, attempt),
				"a start prepared before shutdown was installed")
			require.Nil(t, GlobalClient())
			assert.Empty(t, staleWriter.Payloads(), "stale start flushed after shutdown")

			stale.startFlushMu.Lock()
			stalePending := stale.startFlushDone
			staleReservations := stale.startFlushReservations
			staleClaimed := stale.startFlushClaimed
			stale.startFlushMu.Unlock()
			assert.Nil(t, stalePending, "stale start retained its pending marker")
			assert.Zero(t, staleReservations, "stale start retained a marker reservation")
			assert.False(t, staleClaimed, "stale start retained a claimed marker")
			select {
			case <-staleDone:
			default:
				t.Fatal("stale start marker was not completed")
			}

			startCountSettled := make(chan struct{})
			go func() {
				attempt.flushes.Wait()
				close(startCountSettled)
			}()
			select {
			case <-startCountSettled:
			case <-time.After(5 * time.Second):
				t.Fatal("stale start did not release its WaitGroup count")
			}

			var freshWriter *internal.RecordWriter
			fresh, freshWriter = newConcreteClient()
			StartApp(fresh)
			require.Eventually(t, func() bool { return GlobalClient() == fresh },
				5*time.Second, 10*time.Millisecond, "fresh StartApp did not install after shutdown")
			require.Eventually(t, func() bool { return len(freshWriter.Payloads()) > 0 },
				5*time.Second, 10*time.Millisecond, "fresh StartApp did not flush after shutdown")
			require.Eventually(t, func() bool {
				fresh.startFlushMu.Lock()
				defer fresh.startFlushMu.Unlock()
				return fresh.startFlushDone == nil
			}, 5*time.Second, 10*time.Millisecond, "fresh startup flush did not complete")
		})
	}
}

// TestStopAppWaitsForEveryPreparedStart proves that registration happens
// before publication. StopApp removes the winner only after both attempts are
// registered, then must keep waiting for the delayed contender to finish.
func TestStopAppWaitsForEveryPreparedStart(t *testing.T) {
	telemetryEnabledOnce = sync.Once{}
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })
	SwapClient(nil)

	newConcreteClient := func() (*client, *internal.RecordWriter) {
		tracerConfig := internal.TracerConfig{Service: "test-service", Env: "test-env", Version: "1.0.0"}
		config := defaultConfig(ClientConfig{})
		config.AgentURL = "http://localhost:8126"
		config.FlushInterval = internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour}
		c, err := newClient(tracerConfig, config)
		require.NoError(t, err)
		writer := &internal.RecordWriter{}
		c.writer = writer
		return c, writer
	}

	winner, winnerWriter := newConcreteClient()
	delayed, delayedWriter := newConcreteClient()
	t.Cleanup(func() {
		SwapClient(nil)
		_ = winner.Close()
		_ = delayed.Close()
	})

	winnerAttempt, empty := prepareStartApp()
	require.True(t, empty)
	delayedAttempt, empty := prepareStartApp()
	require.True(t, empty)
	winner.AppStart()
	delayed.AppStart()
	winnerDone := winner.markStartFlushPending()
	delayedDone := delayed.markStartFlushPending()
	require.NotNil(t, winnerDone)
	require.NotNil(t, delayedDone)

	winnerHandedOff := false
	defer func() {
		if !winnerHandedOff {
			winnerAttempt.done()
			completeLosingStartFlush(winner, winnerDone)
		}
	}()
	delayedHandedOff := false
	defer func() {
		if !delayedHandedOff {
			delayedAttempt.done()
			completeLosingStartFlush(delayed, delayedDone)
		}
	}()

	winnerHandedOff = true
	require.True(t, finishStartApp(winner, winner, winnerDone, winnerAttempt))
	require.Eventually(t, func() bool { return len(winnerWriter.Payloads()) > 0 },
		5*time.Second, 10*time.Millisecond, "winning startup flush did not run")
	require.Same(t, winner, GlobalClient())

	stopped := make(chan struct{})
	go func() {
		StopApp()
		close(stopped)
	}()
	require.Eventually(t, func() bool { return GlobalClient() == nil },
		5*time.Second, 10*time.Millisecond, "StopApp did not remove the winner")
	select {
	case <-stopped:
		t.Error("StopApp returned before the delayed prepared start released its registration")
	case <-time.After(100 * time.Millisecond):
	}

	delayedHandedOff = true
	require.False(t, finishStartApp(delayed, delayed, delayedDone, delayedAttempt),
		"the delayed start installed after StopApp")
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("StopApp did not return after every prepared start finished")
	}
	assert.Empty(t, delayedWriter.Payloads(), "delayed stale start flushed after StopApp")
	delayed.startFlushMu.Lock()
	delayedPending := delayed.startFlushDone
	delayedReservations := delayed.startFlushReservations
	delayed.startFlushMu.Unlock()
	assert.Nil(t, delayedPending)
	assert.Zero(t, delayedReservations)
}

type startAppRaceClient struct {
	Client
	arrived chan<- struct{}
	release <-chan struct{}
	flushes atomic.Int32
	closes  atomic.Int32
}

func (c *startAppRaceClient) AppStart() {
	c.arrived <- struct{}{}
	<-c.release
	c.Client.AppStart()
}

func (c *startAppRaceClient) Flush() {
	c.flushes.Add(1)
	c.Client.Flush()
}

func (c *startAppRaceClient) Close() error {
	c.closes.Add(1)
	return c.Client.Close()
}

// TestConcurrentStartAppDifferentClientsInstallsOnlyWinner deterministically
// drives two StartApp calls past the initial empty-slot check. Exactly one
// caller may install, activate, and flush its client; the loser must neither
// replace the winner nor close either client.
func TestConcurrentStartAppDifferentClientsInstallsOnlyWinner(t *testing.T) {
	telemetryEnabledOnce = sync.Once{}
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })
	SwapClient(nil)

	newRaceClient := func(arrived chan<- struct{}, release <-chan struct{}) *startAppRaceClient {
		tracerConfig := internal.TracerConfig{Service: "test-service", Env: "test-env", Version: "1.0.0"}
		config := defaultConfig(ClientConfig{})
		config.AgentURL = "http://localhost:8126"
		config.FlushInterval = internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour}
		base, err := newClient(tracerConfig, config)
		require.NoError(t, err)
		base.writer = &internal.RecordWriter{}
		return &startAppRaceClient{Client: base, arrived: arrived, release: release}
	}

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	first := newRaceClient(arrived, release)
	second := newRaceClient(arrived, release)
	defer func() {
		SwapClient(nil)
		_ = first.Client.Close()
		_ = second.Client.Close()
	}()

	var started sync.WaitGroup
	started.Go(func() { StartApp(first) })
	started.Go(func() { StartApp(second) })
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent StartApp calls did not both reach AppStart")
		}
	}
	close(release)
	started.Wait()

	installed := GlobalClient()
	require.True(t, installed == first || installed == second)
	require.Eventually(t, func() bool {
		return first.flushes.Load()+second.flushes.Load() == 1
	}, 5*time.Second, 10*time.Millisecond, "exactly one StartApp caller must flush")
	assert.Zero(t, first.closes.Load(), "StartApp closed the first client")
	assert.Zero(t, second.closes.Load(), "StartApp closed the second client")
}

// TestConcurrentStartAppSingleClientKeepsFlushWorking verifies that two
// concurrent StartApp calls on the same client leave a working client: the
// call that loses the installation must not overwrite the flush channel of
// the winner, because a later Close would then wait for a channel that
// nothing closes, run into its bound, and cancel the writer of the installed
// client.
func TestConcurrentStartAppSingleClientKeepsFlushWorking(t *testing.T) {
	// Force telemetry enabled: StartApp ignores calls while Disabled.
	telemetryEnabledOnce = sync.Once{}
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })
	t.Cleanup(func() { SwapClient(nil) })
	SwapClient(nil)

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tracerConfig := internal.TracerConfig{Service: "test-service", Env: "test-env", Version: "1.0.0"}
	config := defaultConfig(ClientConfig{
		AgentURL:   srv.URL,
		HTTPClient: &http.Client{Timeout: time.Second},
	})
	config.FlushInterval = internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour}
	c, err := newClient(tracerConfig, config)
	require.NoError(t, err)

	// Race two installations of the same client.
	var barrier, started sync.WaitGroup
	barrier.Add(1)
	for range 2 {
		started.Go(func() {
			barrier.Wait()
			StartApp(c)
		})
	}
	barrier.Done()
	started.Wait()

	// The flush of the winning installation finishes: the client either
	// already cleared its pending channel or the pending channel is closed.
	require.Eventually(t, func() bool {
		c.startFlushMu.Lock()
		flushDone := c.startFlushDone
		c.startFlushMu.Unlock()
		if flushDone == nil {
			return true
		}
		select {
		case <-flushDone:
			return true
		default:
			return false
		}
	}, 10*time.Second, 10*time.Millisecond)

	select {
	case <-c.flushTicker.Done():
		t.Fatal("concurrent StartApp stopped the ticker of the installed client")
	default:
	}

	// The client still sends. A canceled writer, which a corrupted Close
	// leaves behind, fails every request.
	c.Flush()
	require.Eventually(t, func() bool { return requests.Load() > 0 }, 5*time.Second, 10*time.Millisecond,
		"the client cannot send after concurrent StartApp: the writer was canceled")
}
