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
	p.mu.Lock()
	done := p.teardownDone
	p.mu.Unlock()
	if done == nil {
		// A test marked the provider shut down without running the teardown.
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
		tb.Errorf("provider teardown did not finish: %v", ctx.Err())
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
	for _, g := range strings.Split(string(buf), "\n\n") {
		for _, line := range strings.Split(g, "\n") {
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
func requireNoStrayProviderGoroutines(tb testing.TB) {
	tb.Helper()
	deadline := time.Now().Add(2 * time.Second)
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
