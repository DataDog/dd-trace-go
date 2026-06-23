// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package waf

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/timer"
	"github.com/stretchr/testify/require"

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
	ctxOp, wafCtx, _ := newSubcontextTestOperation(t)
	subOp := ctxOp.NewSubcontextOp()
	require.NotNil(t, subOp.subcontext)

	oversizedURL := "http://example.com/" + strings.Repeat("a", 70000)
	subOp.Run(ctxOp, addresses.RunAddressData{
		Data: map[string]any{
			addresses.ServerIONetURLAddr: oversizedURL,
		},
		Scope:     addresses.RASPScope,
		Ephemeral: true,
	})

	require.NotEmpty(t, subOp.subcontext.Truncations().StringTooLong)
	require.True(t, wafCtx.Truncations().IsEmpty(), "subcontext run must not write truncations on the parent context")

	subOp.Close()
	require.Nil(t, subOp.subcontext)
	require.NotEmpty(t, ctxOp.GetMetricsInstance().MergedTruncations(libddwaf.Truncations{}).StringTooLong)
}

func TestSubcontextOperation_aggregates_truncations_and_external_rasp_timer(t *testing.T) {
	ctxOp, _, metrics := newSubcontextTestOperation(t)
	subOp := ctxOp.NewSubcontextOp()
	require.NotNil(t, subOp.subcontext)

	subOp.Run(ctxOp, addresses.RunAddressData{
		Data: map[string]any{
			addresses.ServerIONetURLAddr: "http://example.com/" + strings.Repeat("b", 70000),
		},
		Scope:     addresses.RASPScope,
		Ephemeral: true,
	})
	subOp.Close()

	merged := metrics.MergedTruncations(libddwaf.Truncations{})
	require.NotEmpty(t, merged.StringTooLong)
	require.Positive(t, metrics.ExternalDuration(addresses.RASPScope, 0))
}

func TestSubcontextOperation_does_not_refire_ssrf_request_on_response_run(t *testing.T) {
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
	ctxOp, _, _ := newSubcontextTestOperation(t)
	ctxOp.SetSupportedAddresses(config.NewAddressSet([]string{addresses.ServerIONetURLAddr}))
	subOp := ctxOp.NewSubcontextOp()
	require.NotNil(t, subOp.subcontext)
	defer subOp.Close()

	data := map[string]any{
		addresses.ServerIONetURLAddr:            ssrfURL,
		addresses.GRPCServerResponseMessageAddr: map[string]any{"unsupported": strings.Repeat("x", 70000)},
	}
	subOp.Run(ctxOp, addresses.RunAddressData{Data: data, Scope: addresses.RASPScope, Ephemeral: true})

	require.Contains(t, data, addresses.ServerIONetURLAddr)
	require.NotContains(t, data, addresses.GRPCServerResponseMessageAddr)
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
		Scope: addresses.WAFScope,
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
		Scope:     addresses.RASPScope,
		Ephemeral: true,
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
		Scope:     addresses.RASPScope,
		Ephemeral: true,
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
