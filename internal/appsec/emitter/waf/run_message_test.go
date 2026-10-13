// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package waf

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/timer"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/limiter"
)

func newMessageRunContext(t testing.TB, address string) context.Context {
	t.Helper()
	if ok, _ := libddwaf.Usable(); !ok {
		t.Skip("WAF cannot be used")
	}
	var rules map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
        "version":"2.2",
        "metadata":{"rules_version":"test"},
        "rules":[{
            "id":"message-rule","name":"Block attack message",
            "tags":{"type":"test","category":"attack_attempt"},
            "conditions":[{"operator":"match_regex","parameters":{
                "inputs":[{"address":"`+address+`"}],"regex":"attack"
            }}],"transformers":[],"on_match":["block"]
        }],
        "actions":[{"id":"block","type":"block_request","parameters":{
            "status_code":403,"type":"auto"
        }}]
    }`), &rules))
	builder, err := libddwaf.NewBuilder()
	require.NoError(t, err)
	t.Cleanup(builder.Close)
	_, err = builder.AddOrUpdateConfig("/message-test", rules)
	require.NoError(t, err)
	handle, err := builder.Build()
	require.NoError(t, err)
	require.NotNil(t, handle)
	t.Cleanup(handle.Close)
	wafCtx, err := handle.NewContext(context.Background(), timer.WithUnlimitedBudget(), timer.WithComponents(addresses.Scopes[:]...))
	require.NoError(t, err)
	t.Cleanup(wafCtx.Close)
	op, ctx := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	t.Cleanup(op.Finish)
	op.SwapContext(wafCtx)
	op.SetSupportedAddresses(config.NewAddressSet(handle.Addresses()))
	op.SetLimiter(limiter.NewTokenTicker(100, 100))
	metrics := NewMetricsInstance(handle, "test")
	op.SetMetricsInstance(metrics.NewContextMetrics())
	return ctx
}

func TestRunSimpleSubcontextUnsupportedMessageDoesNotAllocate(t *testing.T) {
	// Requests are inspected, responses are not, as in the default ruleset.
	ctx := newMessageRunContext(t, addresses.GRPCServerRequestMessageAddr)
	data := addresses.RunAddressData{Data: make(map[string]any, 1), TimerKey: addresses.WAFScope}
	var runErr error
	allocs := testing.AllocsPerRun(100, func() {
		// Reuse the map so the assertion measures instrumentation, not the caller.
		data.Data[addresses.GRPCServerResponseMessageAddr] = "attack"
		runErr = RunSimpleSubcontext(ctx, data, "missing test operation")
	})
	require.NoError(t, runErr)
	require.Zero(t, allocs, "unsupported streamed messages must not allocate native WAF subcontexts or Go instrumentation")
}

func TestRunSimpleSubcontextSupportedMessagesStillBlockAndAreIsolated(t *testing.T) {
	for _, addr := range []string{addresses.GRPCServerRequestMessageAddr, addresses.GRPCServerResponseMessageAddr} {
		t.Run(addr, func(t *testing.T) {
			ctx := newMessageRunContext(t, addr)
			for _, message := range []string{"safe", "attack", "safe", "attack"} {
				// Include an unsupported address to cover filtering a mixed batch.
				data := addresses.RunAddressData{Data: map[string]any{
					addr: message, "unsupported.test.address": "attack",
				}, TimerKey: addresses.WAFScope}
				err := RunSimpleSubcontext(ctx, data, "missing test operation")
				if message == "attack" {
					var block *events.BlockingSecurityEvent
					require.ErrorAs(t, err, &block, "a configured message rule must still block")
				} else {
					require.NoError(t, err, "previous message data must not survive in a later subcontext")
				}
			}
		})
	}
}

func BenchmarkRunSimpleSubcontextMessages(b *testing.B) {
	for _, tc := range []struct{ name, ruleAddress string }{
		{"unsupported-response", addresses.GRPCServerRequestMessageAddr},
		{"supported-response", addresses.GRPCServerResponseMessageAddr},
	} {
		b.Run(tc.name, func(b *testing.B) {
			ctx := newMessageRunContext(b, tc.ruleAddress)
			data := addresses.RunAddressData{Data: make(map[string]any, 1), TimerKey: addresses.WAFScope}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				data.Data[addresses.GRPCServerResponseMessageAddr] = "safe"
				if err := RunSimpleSubcontext(ctx, data, "missing test operation"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
