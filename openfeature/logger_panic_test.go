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

// TestProviderGoroutines_FindsRunningProvider guards providerGoroutines
// against renames: if it stops matching the provider's goroutines, leak
// detection is silently disabled.
func TestProviderGoroutines_FindsRunningProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	evp := testAgentlessEVPClient(t, nil, srv, "test-api-key")
	p := newDatadogProviderWithSourceAndEVP(ProviderConfig{}, internalffe.SourceAgentless, evp)
	cleanupProvider(t, p)

	p.updateConfiguration(createTestConfig())
	if len(providerGoroutines()) == 0 {
		t.Fatal("providerGoroutines found no goroutines of a running provider")
	}
}
