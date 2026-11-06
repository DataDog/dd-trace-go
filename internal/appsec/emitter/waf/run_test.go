// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package waf

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/waferrors"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/limiter"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// errRunner is a [libddwaf.Runner] that always returns err.
type errRunner struct{ err error }

func (r errRunner) Run(context.Context, libddwaf.RunAddressData) (libddwaf.Result, error) {
	return libddwaf.Result{}, r.err
}

// sumMetric adds the values of all the AppSec metrics called name that have all
// the given tags. It sums values instead of looking for the absence of a metric:
// some metrics, like rasp.timeout, exist with the value 0 after the metrics
// instance creation.
func sumMetric(client *telemetrytest.RecordClient, name string, tags ...string) float64 {
	var sum float64
	for key, handle := range client.Metrics {
		if key.Namespace != telemetry.NamespaceAppSec || key.Name != name {
			continue
		}
		keyTags := strings.Split(key.Tags, ",")
		if !slices.ContainsFunc(tags, func(tag string) bool { return !slices.Contains(keyTags, tag) }) {
			sum += handle.Get()
		}
	}
	return sum
}

// hasExceptionLog returns true if the client received the telemetry log that
// [ContextMetrics.RecordException] emits.
func hasExceptionLog(client *telemetrytest.RecordClient) bool {
	return slices.ContainsFunc(client.Logs, func(line telemetrytest.LogLine) bool {
		return line.Level == telemetry.LogError && line.Text == "appsec exception"
	})
}

// requireNoRASPErrorCodes checks that no RASP rule type has an error code.
func requireNoRASPErrorCodes(t *testing.T, metrics *ContextMetrics) {
	t.Helper()
	for ruleType := range metrics.RASPErrorCodes {
		require.Zero(t, metrics.RASPErrorCodes[ruleType].Load(), "rule type %d", ruleType)
	}
}

// newRunWAFTestOperation returns a context operation that has a fresh metrics
// instance and that supports the given addresses. The caller must call the
// returned function to restore the global telemetry client and to finish the
// operation.
func newRunWAFTestOperation(client *telemetrytest.RecordClient, supported ...string) (*ContextOperation, *ContextMetrics, func()) {
	restoreClient := telemetry.MockClient(client)
	op, _ := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	op.SetLimiter(limiter.NewTokenTicker(100, 100))
	op.SetSupportedAddresses(config.NewAddressSet(supported))
	handleMetrics := NewMetricsInstance(nil, "test")
	metrics := handleMetrics.NewContextMetrics()
	op.SetMetricsInstance(metrics)
	return op, metrics, func() {
		op.Finish()
		restoreClient()
	}
}

// sqliAddresses returns the addresses of a SQL injection RASP run.
func sqliAddresses() addresses.RunAddressData {
	return addresses.NewAddressesBuilder().WithDBStatement("SELECT 1").WithDBType("postgresql").Build()
}

// wafScopeAddresses returns the addresses of a WAF scope run.
func wafScopeAddresses() addresses.RunAddressData {
	return addresses.RunAddressData{TimerKey: addresses.WAFScope}
}

var sqliSupportedAddresses = []string{addresses.ServerDBStatementAddr, addresses.ServerDBTypeAddr}

// sqliRuleTypeTag is the tag of the SQL injection RASP metrics.
const sqliRuleTypeTag = "rule_type:sql_injection"

// TestRunWAFTimeoutIsNotAnError checks that a WAF timeout is counted only as a
// timeout, and that the other errors are still counted as errors.
func TestRunWAFTimeoutIsNotAnError(t *testing.T) {
	wrappedTimeout := fmt.Errorf("encode: %w", waferrors.ErrTimeout)

	requireRASPTimeout := func(t *testing.T, client *telemetrytest.RecordClient, metrics *ContextMetrics) {
		t.Helper()
		require.Equal(t, 1.0, sumMetric(client, "rasp.timeout", sqliRuleTypeTag))
		require.EqualValues(t, 1, metrics.SumRASPTimeouts[addresses.RASPRuleTypeSQLI].Load())
		require.Zero(t, sumMetric(client, "rasp.error"))
		require.Zero(t, sumMetric(client, "waf.error"))
		requireNoRASPErrorCodes(t, metrics)
		require.Empty(t, metrics.ExceptionType())
	}
	requireWAFTimeout := func(t *testing.T, client *telemetrytest.RecordClient, metrics *ContextMetrics) {
		t.Helper()
		require.Zero(t, sumMetric(client, "waf.error"))
		require.Zero(t, sumMetric(client, "rasp.error"))
		require.Equal(t, 1.0, sumMetric(client, "waf.requests", "waf_timeout:true", "waf_error:false"))
		require.Zero(t, metrics.WAFErrorCode.Load())
		require.EqualValues(t, 1, metrics.SumWAFTimeouts.Load())
		require.Empty(t, metrics.ExceptionType())
	}
	requireInstrumentationException := func(t *testing.T, client *telemetrytest.RecordClient, metrics *ContextMetrics) {
		t.Helper()
		require.Equal(t, ExceptionTypeInstrumentation, metrics.ExceptionType())
		require.True(t, hasExceptionLog(client), "logs: %v", client.Logs)
		require.Zero(t, sumMetric(client, "rasp.error"))
		require.Zero(t, sumMetric(client, "waf.error"))
		requireNoRASPErrorCodes(t, metrics)
		require.Zero(t, metrics.WAFErrorCode.Load())
	}

	for _, tc := range []struct {
		name      string
		addrs     func() addresses.RunAddressData
		supported []string
		err       error
		check     func(*testing.T, *telemetrytest.RecordClient, *ContextMetrics)
	}{
		{
			name:      "rasp/timeout",
			addrs:     sqliAddresses,
			supported: sqliSupportedAddresses,
			err:       waferrors.ErrTimeout,
			check:     requireRASPTimeout,
		},
		{
			name:      "rasp/wrapped-timeout",
			addrs:     sqliAddresses,
			supported: sqliSupportedAddresses,
			err:       wrappedTimeout,
			check:     requireRASPTimeout,
		},
		{
			name:  "waf/timeout",
			addrs: wafScopeAddresses,
			err:   waferrors.ErrTimeout,
			check: requireWAFTimeout,
		},
		{
			name:  "waf/wrapped-timeout",
			addrs: wafScopeAddresses,
			err:   wrappedTimeout,
			check: requireWAFTimeout,
		},
		{
			// The URL address is not supported, so runWAF removes it. The
			// remaining addresses do not match any RASP rule type.
			name: "rasp/unknown-rule-type/timeout",
			addrs: func() addresses.RunAddressData {
				return addresses.NewAddressesBuilder().
					WithDownwardURL("http://example.com").
					WithDownwardRequestHeaders(map[string][]string{"a": {"b"}}).
					Build()
			},
			supported: []string{addresses.ServerIONetRequestHeadersAddr},
			err:       waferrors.ErrTimeout,
			check:     requireInstrumentationException,
		},
		{
			name: "unknown-scope/timeout",
			addrs: func() addresses.RunAddressData {
				return addresses.RunAddressData{TimerKey: "unknown"}
			},
			err:   waferrors.ErrTimeout,
			check: requireInstrumentationException,
		},
		{
			name:      "rasp/internal-error",
			addrs:     sqliAddresses,
			supported: sqliSupportedAddresses,
			err:       waferrors.ErrInternal,
			check: func(t *testing.T, client *telemetrytest.RecordClient, metrics *ContextMetrics) {
				require.Equal(t, 1.0, sumMetric(client, "rasp.error", "waf_error:1", sqliRuleTypeTag))
				require.Equal(t, 1.0, sumMetric(client, "rasp.error"))
				require.Zero(t, sumMetric(client, "rasp.timeout"))
				require.EqualValues(t, 1, metrics.RASPErrorCodes[addresses.RASPRuleTypeSQLI].Load())
				require.Equal(t, ExceptionTypeRASP, metrics.ExceptionType())
			},
		},
		{
			name:  "waf/internal-error",
			addrs: wafScopeAddresses,
			err:   waferrors.ErrInternal,
			check: func(t *testing.T, client *telemetrytest.RecordClient, metrics *ContextMetrics) {
				require.Equal(t, 1.0, sumMetric(client, "waf.error", "waf_error:1"))
				require.Equal(t, 1.0, sumMetric(client, "waf.error"))
				require.Equal(t, 1.0, sumMetric(client, "waf.requests", "waf_timeout:false", "waf_error:true"))
				require.EqualValues(t, 1, metrics.WAFErrorCode.Load())
				require.Equal(t, ExceptionTypeWAF, metrics.ExceptionType())
			},
		},
		{
			name:  "waf/non-run-error",
			addrs: wafScopeAddresses,
			err:   errors.New("boom"),
			check: func(t *testing.T, client *telemetrytest.RecordClient, metrics *ContextMetrics) {
				require.Equal(t, 1.0, sumMetric(client, "waf.error", "waf_error:-127"))
				require.Equal(t, 1.0, sumMetric(client, "waf.error"))
				require.EqualValues(t, defaultWafErrorCode, metrics.WAFErrorCode.Load())
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			op, metrics, cleanup := newRunWAFTestOperation(client, tc.supported...)
			defer cleanup()

			op.runWAF(op, errRunner{tc.err}, tc.addrs(), false)
			metrics.Submit(libddwaf.Truncations{}, nil)

			tc.check(t, client, metrics)
		})
	}
}

// TestRunWAFErrorAndTimeoutOrder checks that, in the same request, a timeout
// neither hides a real error nor is counted as one, whatever the order.
func TestRunWAFErrorAndTimeoutOrder(t *testing.T) {
	for _, order := range []struct {
		name string
		errs []error
	}{
		{name: "error-then-timeout", errs: []error{waferrors.ErrInternal, waferrors.ErrTimeout}},
		{name: "timeout-then-error", errs: []error{waferrors.ErrTimeout, waferrors.ErrInternal}},
	} {
		t.Run("rasp/"+order.name, func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			op, metrics, cleanup := newRunWAFTestOperation(client, sqliSupportedAddresses...)
			defer cleanup()

			for _, err := range order.errs {
				op.runWAF(op, errRunner{err}, sqliAddresses(), false)
			}
			metrics.Submit(libddwaf.Truncations{}, nil)

			require.Equal(t, 1.0, sumMetric(client, "rasp.error", "waf_error:1", sqliRuleTypeTag))
			require.Equal(t, 1.0, sumMetric(client, "rasp.error"))
			require.Equal(t, 1.0, sumMetric(client, "rasp.timeout", sqliRuleTypeTag))
			require.EqualValues(t, 1, metrics.RASPErrorCodes[addresses.RASPRuleTypeSQLI].Load())
		})

		t.Run("waf/"+order.name, func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			op, metrics, cleanup := newRunWAFTestOperation(client)
			defer cleanup()

			for _, err := range order.errs {
				op.runWAF(op, errRunner{err}, wafScopeAddresses(), false)
			}
			metrics.Submit(libddwaf.Truncations{}, nil)

			require.Equal(t, 1.0, sumMetric(client, "waf.error", "waf_error:1"))
			require.Equal(t, 1.0, sumMetric(client, "waf.error"))
			require.Equal(t, 1.0, sumMetric(client, "waf.requests", "waf_timeout:true", "waf_error:true"))
			require.EqualValues(t, 1, metrics.WAFErrorCode.Load())
			require.EqualValues(t, 1, metrics.SumWAFTimeouts.Load())
		})
	}
}

// countingLimiter is a [limiter.Limiter] that counts its Allow calls. It denies
// when deny is true.
type countingLimiter struct {
	deny  atomic.Bool
	calls atomic.Int64
}

func (l *countingLimiter) Allow() bool {
	l.calls.Add(1)
	// Let the other goroutines run, so that a missing lock shows up in the concurrent tests.
	runtime.Gosched()
	return !l.deny.Load()
}

// fakeRunner is a [libddwaf.Runner] that always returns the same result.
type fakeRunner struct{ result libddwaf.Result }

func (r fakeRunner) Run(context.Context, libddwaf.RunAddressData) (libddwaf.Result, error) {
	return r.result, nil
}

var (
	// keepEventRunner returns 1 event and keep=true.
	keepEventRunner = fakeRunner{libddwaf.Result{Events: []any{"event"}, Keep: true}}
	// keepNoEventRunner returns keep=true and no event, like a trace tagging rule.
	keepNoEventRunner = fakeRunner{libddwaf.Result{Keep: true}}
	// eventRunner returns 1 event and keep=false.
	eventRunner = fakeRunner{libddwaf.Result{Events: []any{"event"}}}
)

// newKeepTestOperation returns a context operation that uses lim, a fresh
// metrics instance and a tag setter that records ext.ManualKeep. The caller must
// call the returned function.
func newKeepTestOperation(t *testing.T, lim *countingLimiter, supported ...string) (*ContextOperation, *recordingTagSetter, *ContextMetrics, *telemetrytest.RecordClient) {
	t.Helper()
	client := new(telemetrytest.RecordClient)
	restoreClient := telemetry.MockClient(client)
	tags := newRecordingTagSetter()
	op, _ := StartContextOperation(context.Background(), tags)
	op.SetLimiter(lim)
	op.SetSupportedAddresses(config.NewAddressSet(supported))
	handleMetrics := NewMetricsInstance(nil, "test")
	metrics := handleMetrics.NewContextMetrics()
	op.SetMetricsInstance(metrics)
	t.Cleanup(func() {
		op.Finish()
		restoreClient()
	})
	return op, tags, metrics, client
}

// requireManualKeep checks if the operation set ext.ManualKeep on the span.
func requireManualKeep(t *testing.T, tags *recordingTagSetter, want bool) {
	t.Helper()
	_, got := tags.Get(ext.ManualKeep)
	require.Equal(t, want, got, "ManualKeep")
}

// requireWAFRequests checks the rate_limited and rule_triggered tags of the only waf.requests series.
func requireWAFRequests(t *testing.T, client *telemetrytest.RecordClient, rateLimited, ruleTriggered bool) {
	t.Helper()
	rateLimitedTag := fmt.Sprintf("rate_limited:%t", rateLimited)
	ruleTriggeredTag := fmt.Sprintf("rule_triggered:%t", ruleTriggered)
	require.Equal(t, 1.0, sumMetric(client, "waf.requests"), "waf.requests count")
	require.Equal(t, 1.0, sumMetric(client, "waf.requests", rateLimitedTag, ruleTriggeredTag), "waf.requests with %s %s", rateLimitedTag, ruleTriggeredTag)
}

// TestRunWAFLimiter checks that the limiter decides only about ManualKeep, for
// a result with keep=true, and that it never removes events.
func TestRunWAFLimiter(t *testing.T) {
	t.Run("keep-allowed", func(t *testing.T) {
		lim := new(countingLimiter)
		op, tags, metrics, client := newKeepTestOperation(t, lim)

		op.runWAF(op, keepEventRunner, wafScopeAddresses(), false)
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 1, lim.calls.Load())
		requireManualKeep(t, tags, true)
		require.Len(t, op.Events(), 1)
		requireWAFRequests(t, client, false, true)
	})

	t.Run("keep-denied", func(t *testing.T) {
		lim := new(countingLimiter)
		lim.deny.Store(true)
		op, tags, metrics, client := newKeepTestOperation(t, lim)

		op.runWAF(op, keepEventRunner, wafScopeAddresses(), false)
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 1, lim.calls.Load())
		requireManualKeep(t, tags, false)
		require.Len(t, op.Events(), 1, "a denial must not remove the events")
		requireWAFRequests(t, client, true, true)
	})

	t.Run("no-keep-with-events", func(t *testing.T) {
		lim := new(countingLimiter)
		lim.deny.Store(true)
		op, tags, metrics, client := newKeepTestOperation(t, lim)

		op.runWAF(op, eventRunner, wafScopeAddresses(), false)
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.Zero(t, lim.calls.Load(), "a result without keep must not use a token")
		requireManualKeep(t, tags, false)
		require.Len(t, op.Events(), 1)
		requireWAFRequests(t, client, false, true)
	})

	t.Run("keep-no-events", func(t *testing.T) {
		lim := new(countingLimiter)
		op, tags, metrics, client := newKeepTestOperation(t, lim)

		op.runWAF(op, keepNoEventRunner, wafScopeAddresses(), false)
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 1, lim.calls.Load())
		requireManualKeep(t, tags, true)
		require.Empty(t, op.Events())
		requireWAFRequests(t, client, false, false)
	})

	t.Run("keep-no-events-denied", func(t *testing.T) {
		lim := new(countingLimiter)
		lim.deny.Store(true)
		op, tags, metrics, client := newKeepTestOperation(t, lim)

		op.runWAF(op, keepNoEventRunner, wafScopeAddresses(), false)
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 1, lim.calls.Load())
		requireManualKeep(t, tags, false)
		requireWAFRequests(t, client, true, false)
	})

	t.Run("cap-only", func(t *testing.T) {
		lim := new(countingLimiter)
		op, tags, metrics, client := newKeepTestOperation(t, lim)

		for range maxWAFEventsPerRequest + 1 {
			op.runWAF(op, keepEventRunner, wafScopeAddresses(), false)
		}
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 1, lim.calls.Load(), "the request is admitted 1 time, then it uses no token")
		requireManualKeep(t, tags, true)
		require.Len(t, op.Events(), maxWAFEventsPerRequest)
		requireWAFRequests(t, client, false, true)
	})

	t.Run("capped-keep-run-request-not-kept-yet", func(t *testing.T) {
		lim := new(countingLimiter)
		op, tags, metrics, client := newKeepTestOperation(t, lim)

		for range maxWAFEventsPerRequest {
			op.runWAF(op, eventRunner, wafScopeAddresses(), false)
		}
		require.Zero(t, lim.calls.Load())
		op.runWAF(op, keepEventRunner, wafScopeAddresses(), false)
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 1, lim.calls.Load(), "a capped keep run can ask for the first admission")
		requireManualKeep(t, tags, true)
		require.Len(t, op.Events(), maxWAFEventsPerRequest)
		requireWAFRequests(t, client, false, true)
	})

	t.Run("denial-then-allowed-then-cap", func(t *testing.T) {
		lim := new(countingLimiter)
		lim.deny.Store(true)
		op, tags, metrics, client := newKeepTestOperation(t, lim)

		op.runWAF(op, keepEventRunner, wafScopeAddresses(), false)
		requireManualKeep(t, tags, false)
		lim.deny.Store(false)
		for range maxWAFEventsPerRequest + 2 {
			op.runWAF(op, keepEventRunner, wafScopeAddresses(), false)
		}
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 2, lim.calls.Load(), "1 denial, 1 admission, then no more call")
		requireManualKeep(t, tags, true)
		require.Len(t, op.Events(), maxWAFEventsPerRequest)
		requireWAFRequests(t, client, true, true)
	})

	t.Run("rasp-keep-denied", func(t *testing.T) {
		lim := new(countingLimiter)
		lim.deny.Store(true)
		op, tags, metrics, client := newKeepTestOperation(t, lim, sqliSupportedAddresses...)

		op.runWAF(op, keepEventRunner, sqliAddresses(), false)
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 1, lim.calls.Load())
		requireManualKeep(t, tags, false)
		require.Len(t, op.Events(), 1)
		requireWAFRequests(t, client, true, false)
	})

	t.Run("rasp-keep-allowed", func(t *testing.T) {
		lim := new(countingLimiter)
		op, tags, metrics, client := newKeepTestOperation(t, lim, sqliSupportedAddresses...)

		op.runWAF(op, keepEventRunner, sqliAddresses(), false)
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 1, lim.calls.Load())
		requireManualKeep(t, tags, true)
		requireWAFRequests(t, client, false, false)
	})

	t.Run("monitor-only-keep-denied", func(t *testing.T) {
		lim := new(countingLimiter)
		lim.deny.Store(true)
		op, tags, metrics, client := newKeepTestOperation(t, lim, sqliSupportedAddresses...)

		op.runWAF(op, keepEventRunner, sqliAddresses(), true)
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 1, lim.calls.Load())
		requireManualKeep(t, tags, false)
		require.Len(t, op.Events(), 1)
		requireWAFRequests(t, client, true, false)
	})

	// The RASP address is unknown here, so RegisterWafRun returns early in its RASP
	// branch. The rate_limited milestone must be set before this return.
	t.Run("rasp-keep-denied-unknown-address", func(t *testing.T) {
		lim := new(countingLimiter)
		lim.deny.Store(true)
		op, tags, metrics, client := newKeepTestOperation(t, lim)

		op.runWAF(op, keepEventRunner, addresses.RunAddressData{TimerKey: addresses.RASPScope}, false)
		metrics.Submit(libddwaf.Truncations{}, nil)

		require.EqualValues(t, 1, lim.calls.Load())
		requireManualKeep(t, tags, false)
		requireWAFRequests(t, client, true, false)
	})
}
