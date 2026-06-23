// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httpsec

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/timer"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	wafemitter "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/limiter"
)

func TestProtectRoundTrip_closes_shared_subcontext_when_ssrf_request_blocks(t *testing.T) {
	handle := newRASPTestHandle(t)
	wafCtx, err := handle.NewContext(context.Background(), timer.WithUnlimitedBudget(), timer.WithComponents(addresses.Scopes[:]...))
	require.NoError(t, err)
	t.Cleanup(wafCtx.Close)

	ctxOp, ctx := wafemitter.StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	t.Cleanup(ctxOp.Finish)
	ctxOp.SwapContext(wafCtx)
	ctxOp.SetSupportedAddresses(config.NewAddressSet(handle.Addresses()))
	ctxOp.SetLimiter(limiter.NewTokenTicker(100, 100))
	handleMetrics := wafemitter.NewMetricsInstance(handle, "1.99.0")
	metrics := handleMetrics.NewContextMetrics()
	ctxOp.SetMetricsInstance(metrics)
	seedRoundTripRequestContext(t, ctxOp)

	handlerOp, _, handlerCtx := StartOperation(ctx, HandlerOperationArgs{Method: http.MethodGet}, tracelib.NoopTagSetter{})
	dyngo.On(handlerOp, func(op *RoundTripOperation, args RoundTripOperationArgs) {
		op.Run(op, addresses.NewAddressesBuilder().
			WithDownwardURL(args.URL).
			WithDownwardMethod(args.Method).
			WithDownwardRequestHeaders(args.Headers).
			Build())
	})

	req, err := http.NewRequestWithContext(handlerCtx, http.MethodGet, "http://169.254.169.254", nil)
	require.NoError(t, err)
	finish, err := ProtectRoundTrip(handlerCtx, req)

	require.Nil(t, finish)
	require.Error(t, err)
	require.True(t, errors.Is(err, &events.BlockingSecurityEvent{}))
	require.Positive(t, metrics.ExternalDuration(addresses.RASPScope, 0), "block path must close the shared subcontext and merge timer stats")
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
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "internal", "appsec", "testdata", "api10.json"))
	require.NoError(t, err)

	var rules map[string]any
	require.NoError(t, json.Unmarshal(raw, &rules))
	return rules
}

func seedRoundTripRequestContext(t *testing.T, ctxOp *wafemitter.ContextOperation) {
	t.Helper()

	ctxOp.Run(ctxOp, addresses.RunAddressData{
		Data: map[string]any{
			addresses.ServerRequestQueryAddr: map[string][]string{
				"payload": {"169.254.169.254"},
			},
		},
		Scope: addresses.WAFScope,
	})
}
