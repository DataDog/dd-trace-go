// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package waf

import (
	"context"
	"testing"

	libddwaf "github.com/DataDog/go-libddwaf/v5"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/limiter"
)

type actionRunner struct{ action string }

func (r actionRunner) Run(context.Context, libddwaf.RunAddressData) (libddwaf.Result, error) {
	return libddwaf.Result{
		Events: []any{"SQL injection"},
		Actions: map[string]any{
			r.action:         map[string]any{},
			"generate_stack": map[string]any{"stack_id": "sql-stack"},
		},
		Keep: true,
	}, nil
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
			require.False(t, metrics.Milestones.requestBlocked)

			// Monitoring does not alter the rules or suppress subsequent protection.
			op.runWAF(op, actionRunner{action}, addresses.RunAddressData{}, false)
			require.Positive(t, blocks)
			require.Equal(t, 2, stacks)
			require.Equal(t, 2, securityEvents)
			if action == "block_request" {
				require.True(t, metrics.Milestones.requestBlocked)
			}
		})
	}
}
