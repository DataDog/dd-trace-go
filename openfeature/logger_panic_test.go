// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	prev := log.GetLevel()
	log.SetLevel(lvl)
	undo := log.UseLogger(panickingLogger{})
	t.Cleanup(func() {
		undo()
		log.SetLevel(prev)
	})
}
