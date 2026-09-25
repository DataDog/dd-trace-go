// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package waf

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/timer"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// wafRequestsTags returns the tags of a waf.requests metric that the "test"
// metrics instance emits with the given milestones.
func wafRequestsTags(requestBlocked, blockFailure, ruleTriggered bool) []string {
	return []string{
		"request_blocked:" + strconv.FormatBool(requestBlocked),
		"block_failure:" + strconv.FormatBool(blockFailure),
		"rule_triggered:" + strconv.FormatBool(ruleTriggered),
		"waf_timeout:false",
		"rate_limited:false",
		"waf_error:false",
		"input_truncated:false",
		"event_rules_version:test",
		"waf_version:" + libddwaf.Version(),
	}
}

// requireBlockOutcome checks that client recorded exactly one waf.requests
// count, with the given block outcome.
func requireBlockOutcome(t *testing.T, client *telemetrytest.RecordClient, ruleTriggered, requestBlocked, blockFailure bool) {
	t.Helper()
	for _, outcome := range []struct{ requestBlocked, blockFailure bool }{
		{false, false},
		{true, false},
		{false, true},
		{true, true},
	} {
		var want float64
		if outcome.requestBlocked == requestBlocked && outcome.blockFailure == blockFailure {
			want = 1
		}
		tags := wafRequestsTags(outcome.requestBlocked, outcome.blockFailure, ruleTriggered)
		require.Equal(t, want, client.Count(telemetry.NamespaceAppSec, "waf.requests", tags).Get(), tags)
	}
}

func TestSubmitBlockOutcome(t *testing.T) {
	tests := []struct {
		name           string
		requested      bool
		failed         bool
		applied        bool
		requestBlocked bool
		blockFailure   bool
	}{
		{name: "not requested"},
		{name: "not requested but unavailable", failed: true},
		{name: "not requested but applied", applied: true},
		{name: "requested with no reported outcome", requested: true, requestBlocked: true},
		{name: "requested and failed", requested: true, failed: true, blockFailure: true},
		{name: "requested and applied", requested: true, applied: true, requestBlocked: true},
		{name: "requested, applied, and failed", requested: true, applied: true, failed: true, requestBlocked: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(client)()
			handleMetrics := NewMetricsInstance(nil, "test")
			metrics := handleMetrics.NewContextMetrics()
			if tc.requested {
				metrics.SetBlockRequested()
			}
			if tc.failed {
				metrics.SetBlockFailed()
			}
			if tc.applied {
				metrics.SetBlockApplied()
			}

			metrics.Submit(libddwaf.Truncations{}, nil)

			requireBlockOutcome(t, client, false, tc.requestBlocked, tc.blockFailure)
		})
	}
}

func TestUpdateClosestToZero(t *testing.T) {
	t.Run("first_error_sets_code", func(t *testing.T) {
		var target atomic.Int32
		updateClosestToZero(&target, -2)
		require.Equal(t, int32(-2), target.Load())
	})

	t.Run("closer_to_zero_replaces", func(t *testing.T) {
		var target atomic.Int32
		updateClosestToZero(&target, -127)
		updateClosestToZero(&target, -2)
		updateClosestToZero(&target, -1)
		require.Equal(t, int32(-1), target.Load())
	})

	t.Run("further_from_zero_ignored", func(t *testing.T) {
		var target atomic.Int32
		updateClosestToZero(&target, -1)
		updateClosestToZero(&target, -127)
		require.Equal(t, int32(-1), target.Load())
	})

	t.Run("equal_code_no_change", func(t *testing.T) {
		var target atomic.Int32
		updateClosestToZero(&target, -1)
		updateClosestToZero(&target, -1)
		require.Equal(t, int32(-1), target.Load())
	})

	t.Run("sentinel_zero_always_replaced", func(t *testing.T) {
		var target atomic.Int32
		// initial zero sentinel — any error code should overwrite it
		updateClosestToZero(&target, -127)
		require.Equal(t, int32(-127), target.Load())
	})
}

// TestRegisterWafRunMilestonesRace exercises concurrent WAF-scope RegisterWafRun calls (which the
// go-libddwaf Context permits for a single request via parallel subcontext/downstream runs) together
// with Submit, which reads the same Milestones. Run with -race.
func TestRegisterWafRunMilestonesRace(t *testing.T) {
	hm := NewMetricsInstance(nil, "test")
	m := hm.NewContextMetrics()

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			m.RegisterWafRun(
				addresses.RunAddressData{TimerKey: addresses.WAFScope},
				nil,
				RequestMilestones{ruleTriggered: true, requestBlocked: true, wafTimeout: true, rateLimited: true, wafError: true},
			)
		})
	}
	wg.Go(func() {
		m.Submit(libddwaf.Truncations{}, nil)
	})
	wg.Wait()
}

// pausingHandle is a metric handle that stops Submit until the test releases it.
type pausingHandle struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (h pausingHandle) Submit(float64) {
	h.entered <- struct{}{}
	<-h.release
}

func (pausingHandle) Get() float64 { return 0 }

// TestSubmitIncludesBlockOutcomeReportedDuringSubmit checks that a block outcome
// reported while Submit runs, before the waf.requests snapshot, is in the
// emitted tags.
func TestSubmitIncludesBlockOutcomeReportedDuringSubmit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failed bool
	}{
		{name: "enforced"},
		{name: "failed", failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(client)()
			hm := NewMetricsInstance(nil, "test")
			// entered is buffered, so the handle never blocks on it.
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseSubmit := func() { releaseOnce.Do(func() { close(release) }) }
			// Submit sends waf.duration_ext before the waf.requests snapshot.
			hm.externalTimerDistributions[addresses.WAFScope] = pausingHandle{entered: entered, release: release}
			m := hm.NewContextMetrics()

			done := make(chan struct{})
			go func() {
				defer close(done)
				m.Submit(libddwaf.Truncations{}, map[timer.Key]time.Duration{timer.Key(addresses.WAFScope): time.Microsecond})
			}()
			// waitSubmit waits for Submit to return, at most once. The wait has a
			// limit, so that a stalled Submit fails the test instead of blocking it.
			var waitOnce sync.Once
			var returned bool
			waitSubmit := func() bool {
				waitOnce.Do(func() {
					select {
					case <-done:
						returned = true
					case <-time.After(10 * time.Second):
						t.Error("Submit did not return after it was released")
					}
				})
				return returned
			}
			// If the test stops early, release Submit and wait for it before the
			// mock telemetry client is restored.
			defer func() {
				releaseSubmit()
				waitSubmit()
			}()

			select {
			case <-entered:
			case <-done:
				t.Fatal("Submit returned without sending waf.duration_ext")
			case <-time.After(10 * time.Second):
				t.Fatal("Submit did not send waf.duration_ext")
			}
			m.SetBlockRequested()
			if tc.failed {
				m.SetBlockFailed()
			}
			releaseSubmit()
			if !waitSubmit() {
				t.FailNow()
			}

			requireBlockOutcome(t, client, false, !tc.failed, tc.failed)
		})
	}
}
