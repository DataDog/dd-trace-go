// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

// The exposure writer, the flag evaluation writer and the shutdown teardown
// all log from goroutines the provider owns. With an application logger that
// panics on every line, none of them may terminate the process, and the
// pending events must still be sent.
func TestProviderGoroutines_ContainLoggerPanics(t *testing.T) {
	var exposures, evaluations atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, exposureEndpoint):
			exposures.Add(1)
		case strings.HasSuffix(r.URL.Path, flagEvalLoggingEndpoint):
			evaluations.Add(1)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	evp := testAgentlessEVPClient(t, nil, srv, "test-api-key")
	p := newDatadogProviderWithSourceAndEVP(ProviderConfig{
		ExposureFlushInterval:       5 * time.Millisecond,
		FlagEvaluationFlushInterval: 5 * time.Millisecond,
	}, internalffe.SourceAgentless, evp)
	cleanupProvider(t, p)
	withPanickingLogger(t, log.LevelDebug)

	p.updateConfiguration(createTestConfig())
	record := func(i int) {
		p.exposureWriter.append(exposureEvent{
			Flag:       exposureFlag{Key: "bool-flag"},
			Allocation: exposureAllocation{Key: "allocation1"},
			Variant:    exposureVariant{Key: "on"},
			Subject:    exposureSubject{ID: "user-" + string(rune('a'+i))},
		})
		p.flagEvalLoggingWriter.record(makeHookContext("bool-flag", "user-1", nil),
			makeEvalDetails("on", of.TargetingMatchReason, "", nil))
	}

	// The writers' periodic flushes send on their own goroutines.
	record(0)
	require.Eventually(t, func() bool { return exposures.Load() > 0 && evaluations.Load() > 0 },
		5*time.Second, time.Millisecond)

	// Shutdown flushes the remaining events from its teardown goroutine.
	sentExposures, sentEvaluations := exposures.Load(), evaluations.Load()
	record(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, p.ShutdownWithContext(ctx))
	assert.Greater(t, exposures.Load(), sentExposures, "shutdown must send the pending exposures")
	assert.Greater(t, evaluations.Load(), sentEvaluations, "shutdown must send the pending evaluations")
}

// The shutdown teardown can finish while the exposure writer's periodic send
// is still in flight. Cleanup must wait for that send, or it logs into the
// logger a later test installs.
func TestShutdownAndWait_JoinsInFlightSend(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, exposureEndpoint) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock) // runs before srv.Close, even if the test fails
	evp := testAgentlessEVPClient(t, nil, srv, "test-api-key")
	p := newDatadogProviderWithSourceAndEVP(ProviderConfig{
		ExposureFlushInterval:       5 * time.Millisecond,
		FlagEvaluationFlushInterval: time.Hour,
	}, internalffe.SourceAgentless, evp)
	cleanupProvider(t, p)

	p.updateConfiguration(createTestConfig())
	p.exposureWriter.append(exposureEvent{Flag: exposureFlag{Key: "bool-flag"}, Subject: exposureSubject{ID: "user-1"}})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the periodic exposure send never started")
	}

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		shutdownAndWait(t, p)
	}()
	// The teardown itself has nothing left to send, so it finishes while the
	// periodic send is still blocked.
	require.Eventually(t, func() bool {
		p.mu.Lock()
		done := p.teardownDone
		p.mu.Unlock()
		if done == nil {
			return false
		}
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 5*time.Second, time.Millisecond)
	select {
	case <-returned:
		t.Fatal("shutdownAndWait returned while the exposure send was in flight")
	case <-time.After(50 * time.Millisecond):
	}

	unblock()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdownAndWait did not return after the send finished")
	}
}

// withPanickingLogger installs, for the rest of the test, an application
// logger that panics on every line, at level lvl.
func withPanickingLogger(t *testing.T, lvl log.Level) {
	t.Helper()
	requireNoStrayProviderGoroutines(t)
	prev := log.GetLevel()
	log.SetLevel(lvl)
	undo := log.UseLogger(panickingLogger{})
	t.Cleanup(func() {
		undo()
		log.SetLevel(prev)
	})
}

// cleanupProvider shuts p down when the test ends and waits for its teardown
// goroutine to finish. Without it, a provider's writers, or a teardown the
// OpenFeature SDK starts when a later test replaces the global provider, keep
// running and logging into whatever logger a later test installs.
func cleanupProvider(tb testing.TB, fp of.FeatureProvider) {
	tb.Helper()
	p, ok := fp.(*DatadogProvider)
	if !ok || p == nil {
		return
	}
	tb.Cleanup(func() { shutdownAndWait(tb, p) })
}

// shutdownAndWait shuts p down, then waits for its teardown goroutine, which
// can outlive ShutdownWithContext when the context expires or when another
// caller, such as the OpenFeature SDK, started the shutdown.
func shutdownAndWait(tb testing.TB, p *DatadogProvider) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = p.ShutdownWithContext(ctx)
	// The teardown can finish before the goroutines it stops have exited: a
	// periodic exposure send may still be in flight, and the poll loop may
	// outlive an expired Stop. Join each of them.
	p.mu.Lock()
	waits := map[string]<-chan struct{}{"teardown": p.teardownDone}
	if p.exposureWriter != nil {
		waits["exposure writer"] = p.exposureWriter.workerDone
	}
	// Gate on ticker, not workerDone: the flag evaluation writer creates
	// workerDone at construction, so a nil check cannot detect "never started".
	if p.flagEvalLoggingWriter != nil && p.flagEvalLoggingWriter.ticker != nil {
		waits["flag evaluation writer"] = p.flagEvalLoggingWriter.workerDone
	}
	if p.agentless != nil {
		waits["agentless poll loop"] = p.agentless.doneCh
	}
	p.mu.Unlock()
	for name, done := range waits {
		if done == nil {
			// Never started, or a test marked the provider shut down
			// without running the teardown.
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			tb.Errorf("provider %s did not exit: %v", name, ctx.Err())
		}
	}
}

// strayProviderGoroutines are the goroutines a provider owns. Any of them
// left running by an earlier test logs into the logger the current test
// installs.
var strayProviderGoroutines = []string{
	"(*exposureWriter).start.func1",
	"(*flagEvalLoggingWriter).start.func1",
	"(*DatadogProvider).ShutdownWithContext.func1",
	"(*agentlessSource).run",
}

// providerGoroutines returns the stacks of the running goroutines listed in
// strayProviderGoroutines.
func providerGoroutines() []string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var found []string
	for g := range strings.SplitSeq(string(buf), "\n\n") {
		for line := range strings.SplitSeq(g, "\n") {
			if strings.HasPrefix(line, "created by ") {
				continue
			}
			if slices.ContainsFunc(strayProviderGoroutines, func(fn string) bool {
				return strings.HasPrefix(line, "github.com/DataDog/dd-trace-go/v2/openfeature."+fn+"(")
			}) {
				found = append(found, g)
				break
			}
		}
	}
	return found
}

// requireNoStrayProviderGoroutines fails the test if goroutines of providers
// from earlier tests are still running. Call it before swapping the global
// logger and before starting the test's own provider: a stray goroutine
// would log into the swapped logger, which can panic, stall or fail
// assertions on what was logged. Use cleanupProvider in the leaking test.
// This targeted stack scan is used instead of goleak: goleak runs at test end
// and flags every goroutine, while this runs mid-suite, names the provider's
// goroutines, and reports the leaking stack.
func requireNoStrayProviderGoroutines(tb testing.TB) {
	tb.Helper()
	// cleanupProvider joins every goroutine through the channel it closes on
	// exit, so a goroutine can still be returning right after the join. A
	// short grace covers that; a leaked goroutine never exits.
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		found := providerGoroutines()
		if len(found) == 0 {
			return
		}
		if time.Now().After(deadline) {
			tb.Fatalf("an earlier test left %d provider goroutine(s) running; shut its provider down with cleanupProvider:\n\n%s",
				len(found), strings.Join(found, "\n\n"))
		}
		time.Sleep(time.Millisecond)
	}
}

// stopAgentlessSource stops a started poll loop at the end of a test. Stop is
// idempotent, so tests that already stop the source can use it too.
func stopAgentlessSource(tb testing.TB, src *agentlessSource) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := src.Stop(ctx); err != nil {
		tb.Errorf("agentless poll loop did not stop: %v", err)
	}
}

// Malformed split-feature diagnostics fire inside json.Unmarshal while
// decoding server-delivered UFC data: both the agentless poll goroutine and
// the Remote Config callback decode it before any outer error branch runs,
// so only the decoder itself can contain a panicking application logger.
// The configuration must still apply: malformed features are dropped, the
// valid ones kept.
func TestMalformedSplitFeatureDecode_ContainsLoggerPanics(t *testing.T) {
	const ufcWithMalformedSplitFeatures = `{
		"createdAt": "2026-10-01T12:00:00Z",
		"format": "SERVER",
		"environment": {"name": "Test"},
		"flags": {
			"split-flag": {"key": "split-flag", "enabled": true, "variationType": "BOOLEAN",
				"variations": {"on": {"key": "on", "value": true}},
				"allocations": [{"key": "alloc-1", "splits": [{"variationKey": "on", "shards": [],
					"features": [
						{"key": "kept", "value": true, "destinations": ["HOOK_EXPOSURE"]},
						{"key": "dropped", "value": {"nested": true}, "destinations": ["HOOK_EXPOSURE"]}
					]}]}]}
		}
	}`

	assertApplied := func(t *testing.T, config *universalFlagsConfiguration) {
		t.Helper()
		require.NotNil(t, config, "a failing logger must not skip applying configuration")
		f := config.Flags["split-flag"]
		require.NotNil(t, f)
		require.Len(t, f.Allocations, 1)
		require.Len(t, f.Allocations[0].Splits, 1)
		features := f.Allocations[0].Splits[0].Features
		if assert.Len(t, features, 1, "the malformed entry must be dropped, the valid one kept") {
			assert.Equal(t, "kept", features[0].Key)
		}
	}

	t.Run("agentless poll", func(t *testing.T) {
		withPanickingLogger(t, log.LevelDebug)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("ETag", `"v7"`)
			_, _ = w.Write([]byte(`{"data":{"id":"1","type":"universal-flag-configuration","attributes":` + ufcWithMalformedSplitFeatures + `}}`))
		}))
		t.Cleanup(srv.Close)
		src := newDebugAgentlessSource(t, srv.URL)
		var applied *universalFlagsConfiguration
		src.apply = func(config *universalFlagsConfiguration) { applied = config }

		require.NotPanics(t, src.poll)

		assertApplied(t, applied)
	})

	t.Run("remote config", func(t *testing.T) {
		// Built before the panicking logger is installed: construction logs a
		// span-enrichment debug line on this goroutine.
		p := newDatadogProvider(ProviderConfig{})
		cleanupProvider(t, p)
		withPanickingLogger(t, log.LevelDebug)

		var status rc.ApplyStatus
		require.NotPanics(t, func() {
			status = processConfigUpdate(p, "datadog/2/FFE_FLAGS/config/config", []byte(ufcWithMalformedSplitFeatures))
		})

		assert.Equal(t, rc.ApplyStateAcknowledged, status.State)
		assertApplied(t, p.getConfiguration())
	})
}

// TestProviderGoroutines_FindsRunningProvider guards providerGoroutines
// against renames: if a matcher stops matching the provider's goroutines,
// leak detection is silently disabled for that goroutine class. Hold every
// goroutine class live at once - both writers, an in-flight agentless poll,
// and a blocked shutdown teardown - and require each matcher to find its
// own goroutine, so one working matcher cannot hide a broken one.
func TestProviderGoroutines_FindsRunningProvider(t *testing.T) {
	t.Setenv(flagEvalCountsEnabledEnvVar, "true")

	// Every request to srv blocks until release, so a goroutine waiting on
	// its response stays live while the test snapshots stacks.
	entered := make(chan string, 16)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- r.URL.Path:
		default:
		}
		<-release
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	// Defers run before cleanups: unblocking first keeps the provider
	// shutdowns below from stalling on a blocked handler if the test fails.
	defer unblock()

	// The agentless provider: its poll goroutine blocks in flight, and both
	// writers start (hourly flushes, so they never touch srv).
	polling, err := startWithAgentless(
		ProviderConfig{ExposureFlushInterval: time.Hour, FlagEvaluationFlushInterval: time.Hour},
		internalffe.Settings{
			AgentlessBaseURL: srv.URL,
			APIKey:           "test-api-key",
			PollInterval:     time.Hour,
			RequestTimeout:   time.Minute,
		})
	require.NoError(t, err)
	cleanupProvider(t, polling)
	polling.updateConfiguration(createTestConfig())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the agentless poll never started")
	}

	// The second provider: its teardown flush is the only send to srv, so the
	// pending event below keeps the teardown goroutine blocked in it, live
	// for the snapshot. Its writers flush hourly and stay running too.
	secondEvp := testAgentlessEVPClient(t, nil, srv, "test-api-key")
	shuttingDown := newDatadogProviderWithSourceAndEVP(ProviderConfig{
		ExposureFlushInterval:       time.Hour,
		FlagEvaluationFlushInterval: time.Hour,
	}, internalffe.SourceAgentless, secondEvp)
	cleanupProvider(t, shuttingDown)
	shuttingDown.updateConfiguration(createTestConfig())
	shuttingDown.exposureWriter.append(exposureEvent{Flag: exposureFlag{Key: "bool-flag"}, Subject: exposureSubject{ID: "user-1"}})
	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownDone <- shuttingDown.ShutdownWithContext(ctx)
	}()

	// The teardown goroutine needs a moment to start, so poll until every
	// matcher has found its goroutine or the deadline expires.
	found := make(map[string]bool, len(strayProviderGoroutines))
	deadline := time.Now().Add(5 * time.Second)
	for len(found) < len(strayProviderGoroutines) && time.Now().Before(deadline) {
		for _, stack := range providerGoroutines() {
			for _, fn := range strayProviderGoroutines {
				if !found[fn] && strings.Contains(stack, "openfeature."+fn+"(") {
					found[fn] = true
				}
			}
		}
		if len(found) < len(strayProviderGoroutines) {
			time.Sleep(time.Millisecond)
		}
	}
	unblock()
	require.NoError(t, <-shutdownDone)

	var missing []string
	for _, fn := range strayProviderGoroutines {
		if !found[fn] {
			missing = append(missing, fn)
		}
	}
	require.Empty(t, missing,
		"providerGoroutines no longer matches these goroutine classes; leak detection is silently disabled for them: %v", missing)
}
