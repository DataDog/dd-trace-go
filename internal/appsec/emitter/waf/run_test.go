// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package waf

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/waferrors"
	"github.com/stretchr/testify/require"

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
				require.Equal(t, 1.0, sumMetric(client, "rasp.error", "waf_error:-3", sqliRuleTypeTag))
				require.Equal(t, 1.0, sumMetric(client, "rasp.error"))
				require.Zero(t, sumMetric(client, "rasp.timeout"))
				require.EqualValues(t, -3, metrics.RASPErrorCodes[addresses.RASPRuleTypeSQLI].Load())
				require.Equal(t, ExceptionTypeRASP, metrics.ExceptionType())
			},
		},
		{
			name:  "waf/internal-error",
			addrs: wafScopeAddresses,
			err:   waferrors.ErrInternal,
			check: func(t *testing.T, client *telemetrytest.RecordClient, metrics *ContextMetrics) {
				require.Equal(t, 1.0, sumMetric(client, "waf.error", "waf_error:-3"))
				require.Equal(t, 1.0, sumMetric(client, "waf.error"))
				require.Equal(t, 1.0, sumMetric(client, "waf.requests", "waf_timeout:false", "waf_error:true"))
				require.EqualValues(t, -3, metrics.WAFErrorCode.Load())
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
				require.EqualValues(t, -127, metrics.WAFErrorCode.Load())
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

			require.Equal(t, 1.0, sumMetric(client, "rasp.error", "waf_error:-3", sqliRuleTypeTag))
			require.Equal(t, 1.0, sumMetric(client, "rasp.error"))
			require.Equal(t, 1.0, sumMetric(client, "rasp.timeout", sqliRuleTypeTag))
			require.EqualValues(t, -3, metrics.RASPErrorCodes[addresses.RASPRuleTypeSQLI].Load())
		})

		t.Run("waf/"+order.name, func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			op, metrics, cleanup := newRunWAFTestOperation(client)
			defer cleanup()

			for _, err := range order.errs {
				op.runWAF(op, errRunner{err}, wafScopeAddresses(), false)
			}
			metrics.Submit(libddwaf.Truncations{}, nil)

			require.Equal(t, 1.0, sumMetric(client, "waf.error", "waf_error:-3"))
			require.Equal(t, 1.0, sumMetric(client, "waf.error"))
			require.Equal(t, 1.0, sumMetric(client, "waf.requests", "waf_timeout:true", "waf_error:true"))
			require.EqualValues(t, -3, metrics.WAFErrorCode.Load())
			require.EqualValues(t, 1, metrics.SumWAFTimeouts.Load())
		})
	}
}
