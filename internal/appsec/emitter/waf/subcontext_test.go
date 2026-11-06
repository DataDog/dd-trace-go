// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package waf

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/timer"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/limiter"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

const ssrfURL = "http://169.254.169.254/latest/meta-data"
const ssrfPayload = "169.254.169.254"

func TestSubcontextOperation_uses_subcontext_and_closes_it(t *testing.T) {
	if ok, _ := libddwaf.Usable(); !ok {
		t.Skip("WAF cannot be used")
	}

	ctxOp, wafCtx, _ := newSubcontextTestOperation(t)
	subOp := ctxOp.NewSubcontextOp()
	require.NotNil(t, subOp.subcontext)

	oversizedURL := "http://example.com/" + strings.Repeat("a", 70000)
	subOp.Run(ctxOp, addresses.RunAddressData{
		Data: map[string]any{
			addresses.ServerIONetURLAddr: oversizedURL,
		},
		TimerKey: addresses.RASPScope,
	})

	require.NotEmpty(t, subOp.subcontext.Truncations().StringTooLong)
	require.True(t, wafCtx.Truncations().IsEmpty(), "subcontext run must not write truncations on the parent context")

	subOp.Close()
	require.Nil(t, subOp.subcontext)
	require.NotEmpty(t, wafCtx.Truncations().StringTooLong)
}

func TestSubcontextOperation_aggregates_truncations_and_external_rasp_timer(t *testing.T) {
	if ok, _ := libddwaf.Usable(); !ok {
		t.Skip("WAF cannot be used")
	}

	ctxOp, wafCtx, _ := newSubcontextTestOperation(t)
	subOp := ctxOp.NewSubcontextOp()
	require.NotNil(t, subOp.subcontext)

	subOp.Run(ctxOp, addresses.RunAddressData{
		Data: map[string]any{
			addresses.ServerIONetURLAddr: "http://example.com/" + strings.Repeat("b", 70000),
		},
		TimerKey: addresses.RASPScope,
	})
	subOp.Close()

	require.NotEmpty(t, wafCtx.Truncations().StringTooLong)
	require.Positive(t, wafCtx.Timer.Stats()[addresses.RASPScope])
}

func TestSubcontextOperation_does_not_refire_ssrf_request_on_response_run(t *testing.T) {
	if ok, _ := libddwaf.Usable(); !ok {
		t.Skip("WAF cannot be used")
	}

	telemetryClient := new(telemetrytest.RecordClient)
	previousClient := telemetry.SwapClient(telemetryClient)
	t.Cleanup(func() { telemetry.SwapClient(previousClient) })

	ctxOp, _, metrics := newSubcontextTestOperation(t)
	seedRequestContext(t, ctxOp)
	subOp := ctxOp.NewSubcontextOp()
	require.NotNil(t, subOp.subcontext)
	defer subOp.Close()

	requestEvalTags := []string{
		"rule_type:ssrf",
		"rule_variant:request",
		"waf_version:" + libddwaf.Version(),
		"event_rules_version:1.99.0",
	}
	responseEvalTags := []string{
		"rule_type:ssrf",
		"rule_variant:response",
		"waf_version:" + libddwaf.Version(),
		"event_rules_version:1.99.0",
	}
	requestEvalBefore := telemetryClient.Count(telemetry.NamespaceAppSec, "rasp.rule.eval", requestEvalTags).Get()
	responseEvalBefore := telemetryClient.Count(telemetry.NamespaceAppSec, "rasp.rule.eval", responseEvalTags).Get()

	subOp.Run(ctxOp, ssrfRequestRunData())
	require.Contains(t, eventRuleIDs(ctxOp.Events()), "rasp-934-100")
	require.Equal(t, uint32(1), metrics.SumRASPCalls.Load())
	require.Equal(t, requestEvalBefore+1, telemetryClient.Count(telemetry.NamespaceAppSec, "rasp.rule.eval", requestEvalTags).Get())

	subOp.Run(ctxOp, ssrfResponseRunData())
	ruleIDs := eventRuleIDs(ctxOp.Events())
	require.Equal(t, 1, countRuleID(ruleIDs, "rasp-934-100"), "response run must not re-fire the request SSRF rule")
	require.Equal(t, uint32(2), metrics.SumRASPCalls.Load(), "request and response variants should each be counted once")
	require.Equal(t, requestEvalBefore+1, telemetryClient.Count(telemetry.NamespaceAppSec, "rasp.rule.eval", requestEvalTags).Get())
	require.Equal(t, responseEvalBefore+1, telemetryClient.Count(telemetry.NamespaceAppSec, "rasp.rule.eval", responseEvalTags).Get())
}

func TestSubcontextOperation_filters_unsupported_addresses_before_encoding(t *testing.T) {
	if ok, _ := libddwaf.Usable(); !ok {
		t.Skip("WAF cannot be used")
	}

	ctxOp, _, _ := newSubcontextTestOperation(t)
	ctxOp.SetSupportedAddresses(config.NewAddressSet([]string{addresses.ServerIONetURLAddr}))
	subOp := ctxOp.NewSubcontextOp()
	require.NotNil(t, subOp.subcontext)
	defer subOp.Close()

	data := map[string]any{
		addresses.ServerIONetURLAddr:            ssrfURL,
		addresses.GRPCServerResponseMessageAddr: map[string]any{"unsupported": strings.Repeat("x", 70000)},
	}
	subOp.Run(ctxOp, addresses.RunAddressData{Data: data, TimerKey: addresses.RASPScope})

	require.Contains(t, data, addresses.ServerIONetURLAddr)
	require.NotContains(t, data, addresses.GRPCServerResponseMessageAddr)
}

func TestSubcontextOperation_nil_context_is_safe_and_records_after_request_skip(t *testing.T) {
	if ok, _ := libddwaf.Usable(); !ok {
		t.Skip("WAF cannot be used")
	}

	telemetryClient := new(telemetrytest.RecordClient)
	previousClient := telemetry.SwapClient(telemetryClient)
	t.Cleanup(func() { telemetry.SwapClient(previousClient) })

	ctxOp, wafCtx, _ := newSubcontextTestOperation(t)
	ctxOp.SwapContext(nil)
	wafCtx.Close()

	skipTags := []string{
		"reason:after-request",
		"rule_type:ssrf",
		"rule_variant:request",
		"waf_version:" + libddwaf.Version(),
		"event_rules_version:1.99.0",
	}
	skipBefore := telemetryClient.Count(telemetry.NamespaceAppSec, "rasp.rule.skipped", skipTags).Get()

	subOp := ctxOp.NewSubcontextOp()
	require.Nil(t, subOp.subcontext)
	require.NotPanics(t, func() {
		subOp.Run(ctxOp, ssrfRequestRunData())
		subOp.Close()
	})
	require.Equal(t, skipBefore+1, telemetryClient.Count(telemetry.NamespaceAppSec, "rasp.rule.skipped", skipTags).Get())
}

func TestSubcontextOperation_Close_is_idempotent_and_does_not_double_add_stats(t *testing.T) {
	if ok, _ := libddwaf.Usable(); !ok {
		t.Skip("WAF cannot be used")
	}

	ctxOp, wafCtx, _ := newSubcontextTestOperation(t)
	subOp := ctxOp.NewSubcontextOp()
	require.NotNil(t, subOp.subcontext)

	subOp.Run(ctxOp, addresses.RunAddressData{
		Data: map[string]any{
			addresses.ServerIONetURLAddr: "http://example.com/" + strings.Repeat("c", 70000),
		},
		TimerKey: addresses.RASPScope,
	})
	subOp.Close()

	truncations := wafCtx.Truncations()
	duration := wafCtx.Timer.Stats()[addresses.RASPScope]
	require.NotEmpty(t, truncations.StringTooLong)
	require.Positive(t, duration)

	require.NotPanics(t, subOp.Close)
	require.Equal(t, truncations, wafCtx.Truncations())
	require.Equal(t, duration, wafCtx.Timer.Stats()[addresses.RASPScope])
}

func TestSubcontextOperation_Close_records_each_scope_duration_from_shared_subcontext(t *testing.T) {
	if ok, _ := libddwaf.Usable(); !ok {
		t.Skip("WAF cannot be used")
	}

	ctxOp, wafCtx, _ := newSubcontextTestOperation(t)
	subOp := ctxOp.NewSubcontextOp()
	require.NotNil(t, subOp.subcontext)

	subOp.Run(ctxOp, addresses.RunAddressData{
		Data: map[string]any{
			addresses.ServerIONetURLAddr: "http://example.com/" + strings.Repeat("d", 70000),
		},
		TimerKey: addresses.RASPScope,
	})
	subOp.Run(ctxOp, addresses.RunAddressData{
		Data:     map[string]any{},
		TimerKey: addresses.WAFScope,
	})
	subOp.Close()

	require.Positive(t, wafCtx.Timer.Stats()[addresses.RASPScope], "later WAF-scope runs must not drop the RASP external duration")
}

func newSubcontextTestOperation(t *testing.T) (*ContextOperation, *libddwaf.Context, *ContextMetrics) {
	t.Helper()

	handle := newRASPTestHandle(t)
	wafCtx, err := handle.NewContext(context.Background(), timer.WithUnlimitedBudget(), timer.WithComponents(addresses.Scopes[:]...))
	require.NoError(t, err)
	t.Cleanup(wafCtx.Close)

	ctxOp, _ := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	t.Cleanup(ctxOp.Finish)
	ctxOp.SwapContext(wafCtx)
	ctxOp.SetSupportedAddresses(config.NewAddressSet(handle.Addresses()))
	ctxOp.SetLimiter(limiter.NewTokenTicker(100, 100))
	handleMetrics := NewMetricsInstance(handle, "1.99.0")
	metrics := handleMetrics.NewContextMetrics()
	ctxOp.SetMetricsInstance(metrics)

	return ctxOp, wafCtx, metrics
}

func newRASPTestHandle(t *testing.T) *libddwaf.Handle {
	t.Helper()

	builder, err := libddwaf.NewBuilder()
	require.NoError(t, err)
	t.Cleanup(builder.Close)

	_, err = builder.AddOrUpdateConfig("/rasp", loadRASPRules(t))
	require.NoError(t, err)

	handle, err := builder.Build()
	require.NoError(t, err)
	require.NotNil(t, handle)
	t.Cleanup(handle.Close)

	return handle
}

func loadRASPRules(t *testing.T) map[string]any {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "testdata", "api10.json"))
	require.NoError(t, err)

	var rules map[string]any
	require.NoError(t, json.Unmarshal(raw, &rules))
	return rules
}

func seedRequestContext(t *testing.T, ctxOp *ContextOperation) {
	t.Helper()

	ctxOp.Run(ctxOp, addresses.RunAddressData{
		Data: map[string]any{
			addresses.ServerRequestQueryAddr: map[string][]string{
				"payload": {ssrfPayload},
			},
		},
		TimerKey: addresses.WAFScope,
	})
}

func ssrfRequestRunData() addresses.RunAddressData {
	return addresses.RunAddressData{
		Data: map[string]any{
			addresses.ServerIONetURLAddr:           ssrfURL,
			addresses.ServerIONetRequestMethodAddr: "GET",
			addresses.ServerIONetRequestHeadersAddr: map[string][]string{
				"host": {"169.254.169.254"},
			},
		},
		TimerKey: addresses.RASPScope,
	}
}

func ssrfResponseRunData() addresses.RunAddressData {
	return addresses.RunAddressData{
		Data: map[string]any{
			addresses.ServerIONetResponseStatusAddr: "200",
			addresses.ServerIONetResponseHeadersAddr: map[string][]string{
				"content-type": {"text/plain"},
			},
		},
		TimerKey: addresses.RASPScope,
	}
}

func eventRuleIDs(events []any) []string {
	ruleIDs := make([]string, 0, len(events))
	for _, event := range events {
		eventMap, ok := event.(map[string]any)
		if !ok {
			continue
		}
		rule, ok := eventMap["rule"].(map[string]any)
		if !ok {
			continue
		}
		id, ok := rule["id"].(string)
		if ok {
			ruleIDs = append(ruleIDs, id)
		}
	}
	return ruleIDs
}

func countRuleID(ruleIDs []string, id string) int {
	count := 0
	for _, ruleID := range ruleIDs {
		if ruleID == id {
			count++
		}
	}
	return count
}

type actionRunner struct{ action string }

func (r actionRunner) Run(context.Context, libddwaf.RunAddressData) (libddwaf.Result, error) {
	actions := map[string]any{"generate_stack": map[string]any{"stack_id": "sql-stack"}}
	if r.action != "" {
		actions[r.action] = map[string]any{}
	}
	return libddwaf.Result{
		Events:  []any{"SQL injection"},
		Actions: actions,
		Keep:    true,
	}, nil
}

func TestRunWAFMonitorOnlyMetrics(t *testing.T) {
	// wantOutcome is the rasp.rule.match block tag for each mode. A redirect is
	// block:irrelevant in both modes, so monitor-only does not change its outcome.
	for _, tc := range []struct {
		action      string
		monitorOnly bool
		wantOutcome string
	}{
		{action: "block_request", monitorOnly: true, wantOutcome: "failure"},
		{action: "block_request", monitorOnly: false, wantOutcome: "success"},
		{action: "redirect_request", monitorOnly: true, wantOutcome: "irrelevant"},
		{action: "redirect_request", monitorOnly: false, wantOutcome: "irrelevant"},
		{action: "", monitorOnly: true, wantOutcome: "irrelevant"},
		{action: "", monitorOnly: false, wantOutcome: "irrelevant"},
	} {
		t.Run(fmt.Sprintf("%s/monitorOnly=%t", tc.action, tc.monitorOnly), func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(client)()
			op, _ := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
			defer op.Finish()
			op.SetLimiter(limiter.NewTokenTicker(100, 100))
			op.SetSupportedAddresses(config.NewAddressSet([]string{addresses.ServerDBStatementAddr, addresses.ServerDBTypeAddr}))
			handleMetrics := NewMetricsInstance(nil, "test")
			metrics := handleMetrics.NewContextMetrics()
			op.SetMetricsInstance(metrics)
			op.runWAF(op, actionRunner{tc.action}, addresses.NewAddressesBuilder().WithDBStatement("SELECT 1").WithDBType("postgresql").Build(), tc.monitorOnly)
			require.EqualValues(t, 1, metrics.SumRASPCalls.Load())
			if tc.monitorOnly {
				require.False(t, metrics.Milestones.requestBlocked)
			}
			for _, outcome := range []string{"failure", "irrelevant", "success"} {
				var want float64
				if outcome == tc.wantOutcome {
					want = 1
				}
				tags := []string{"block:" + outcome, "rule_type:sql_injection", "waf_version:" + libddwaf.Version(), "event_rules_version:test"}
				require.Equal(t, want, client.Count(telemetry.NamespaceAppSec, "rasp.rule.match", tags).Get(), outcome)
			}
		})
	}
}

func TestSubcontextOperationMonitorOnlyBlockFailure(t *testing.T) {
	if ok, _ := libddwaf.Usable(); !ok {
		t.Skip("WAF cannot be used")
	}
	client := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(client)()
	op, _, metrics := newSubcontextTestOperation(t)
	seedRequestContext(t, op)
	var blocks, stacks int
	dyngo.OnData(op, func(*events.BlockingSecurityEvent) { blocks++ })
	dyngo.OnData(op, func(*actions.StackTraceAction) { stacks++ })
	sub := op.NewSubcontextOp()
	sub.RunMonitorOnly(op, ssrfRequestRunData())
	sub.Close()
	require.Zero(t, blocks)
	require.Positive(t, stacks)
	require.Contains(t, eventRuleIDs(op.Events()), "rasp-934-100")
	require.False(t, metrics.Milestones.requestBlocked)
	for _, outcome := range []string{"failure", "success", "irrelevant"} {
		tags := []string{"block:" + outcome, "rule_type:ssrf", "rule_variant:request", "waf_version:" + libddwaf.Version(), "event_rules_version:1.99.0"}
		var want float64
		if outcome == "failure" {
			want = 1
		}
		require.Equal(t, want, client.Count(telemetry.NamespaceAppSec, "rasp.rule.match", tags).Get())
	}
	sub = op.NewSubcontextOp()
	sub.Run(op, ssrfRequestRunData())
	sub.Close()
	require.Positive(t, blocks)
	require.EqualValues(t, 2, metrics.SumRASPCalls.Load())
	tags := []string{"block:success", "rule_type:ssrf", "rule_variant:request", "waf_version:" + libddwaf.Version(), "event_rules_version:1.99.0"}
	require.EqualValues(t, 1, client.Count(telemetry.NamespaceAppSec, "rasp.rule.match", tags).Get())
}

// TestRunWAFMonitorOnlyWAFRequests checks the emitted waf.requests tags. A
// monitor-only block must not count as a requested block, so it must not report
// block_failure:true or hide a later real block of the same request. A redirect
// is never a requested block.
func TestRunWAFMonitorOnlyWAFRequests(t *testing.T) {
	for _, action := range []string{"block_request", "redirect_request"} {
		for _, tc := range []struct {
			name      string
			laterReal bool
		}{
			{name: "monitor-only"},
			{name: "monitor-only then real", laterReal: true},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				client := new(telemetrytest.RecordClient)
				defer telemetry.MockClient(client)()
				op, _ := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
				defer op.Finish()
				op.SetLimiter(limiter.NewTokenTicker(100, 100))
				handleMetrics := NewMetricsInstance(nil, "test")
				metrics := handleMetrics.NewContextMetrics()
				op.SetMetricsInstance(metrics)

				addrs := addresses.RunAddressData{TimerKey: addresses.WAFScope}
				op.runWAF(op, actionRunner{action}, addrs, true)
				if tc.laterReal {
					op.runWAF(op, actionRunner{action}, addrs, false)
				}
				metrics.Submit(libddwaf.Truncations{}, nil)

				requireBlockOutcome(t, client, true, tc.laterReal && action == "block_request", false)
			})
		}
	}
}

func TestRunWAFMonitorOnlyActions(t *testing.T) {
	for _, action := range []string{"block_request", "redirect_request"} {
		t.Run(action, func(t *testing.T) {
			op, _ := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
			defer op.Finish()
			op.SetLimiter(limiter.NewTokenTicker(100, 100))
			metrics := &ContextMetrics{}
			op.SetMetricsInstance(metrics)
			var blocks, stacks, securityEvents int
			dyngo.OnData(op, func(*events.BlockingSecurityEvent) { blocks++ })
			dyngo.OnData(op, func(*actions.StackTraceAction) { stacks++ })
			dyngo.OnData(op, func(*SecurityEvent) { securityEvents++ })
			op.runWAF(op, actionRunner{action}, addresses.RunAddressData{}, true)
			require.Zero(t, blocks)
			require.Equal(t, 1, stacks)
			require.Equal(t, 1, securityEvents)
			require.Equal(t, []any{"SQL injection"}, op.Events())

			// Monitoring does not alter the rules or suppress subsequent protection.
			op.runWAF(op, actionRunner{action}, addresses.RunAddressData{}, false)
			require.Positive(t, blocks)
			require.Equal(t, 2, stacks)
			require.Equal(t, 2, securityEvents)
		})
	}
}

// TestRunWAFMarksReportedBlock checks that only a WAF-scope block_request that
// can block the request creates the HTTP block whose outcome waf.requests
// reports. A redirect or a RASP-scope block must not change that outcome.
func TestRunWAFMarksReportedBlock(t *testing.T) {
	for _, tc := range []struct {
		name        string
		action      string
		addrs       addresses.RunAddressData
		monitorOnly bool
		wantBlock   bool
		wantReports bool
	}{
		{name: "waf block", action: "block_request", addrs: addresses.RunAddressData{TimerKey: addresses.WAFScope}, wantBlock: true, wantReports: true},
		{name: "waf redirect", action: "redirect_request", addrs: addresses.RunAddressData{TimerKey: addresses.WAFScope}, wantBlock: true},
		{name: "rasp block", action: "block_request", addrs: ssrfRequestRunData(), wantBlock: true},
		{name: "monitor-only waf block", action: "block_request", addrs: addresses.RunAddressData{TimerKey: addresses.WAFScope}, monitorOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op, _ := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
			defer op.Finish()
			op.SetLimiter(limiter.NewTokenTicker(100, 100))
			op.SetSupportedAddresses(config.NewAddressSet([]string{
				addresses.ServerIONetURLAddr,
				addresses.ServerIONetRequestMethodAddr,
				addresses.ServerIONetRequestHeadersAddr,
			}))
			handleMetrics := NewMetricsInstance(nil, "test")
			op.SetMetricsInstance(handleMetrics.NewContextMetrics())
			var blocks []*actions.BlockHTTP
			dyngo.OnData(op, func(a *actions.BlockHTTP) { blocks = append(blocks, a) })

			op.runWAF(op, actionRunner{tc.action}, tc.addrs, tc.monitorOnly)

			if !tc.wantBlock {
				require.Empty(t, blocks)
				return
			}
			require.Len(t, blocks, 1)
			require.Equal(t, tc.wantReports, blocks[0].ReportsBlockOutcome())
		})
	}
}
