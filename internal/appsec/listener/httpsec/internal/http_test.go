package internal

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

import (
	"context"
	"net/http"
	"testing"
	"time"

	internal "github.com/DataDog/appsec-internal-go/appsec"
	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	emitter "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	listener "github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/httpsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/waf"
	libddwaf "github.com/DataDog/go-libddwaf/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "embed" // For go:embed
)

var (
	//go:embed rules.blocking.json
	blockingRules []byte
	//go:embed rules.irrelevant.json
	irrelevantRules []byte
)

func TestFeature_headerCollection(t *testing.T) {
	if ok, err := libddwaf.Usable(); !ok {
		t.Skipf("Skipping tests because libddwaf is not available: %v", err)
	}

	appsec.Start()
	defer appsec.Stop()

	blockingRulesWAFManager, err := config.NewWAFManager(internal.ObfuscatorConfig{}, blockingRules)
	require.NoError(t, err)

	irrelevantRulesWAFManager, err := config.NewWAFManager(internal.ObfuscatorConfig{}, irrelevantRules)
	require.NoError(t, err)

	var (
		request = emitter.HandlerOperationArgs{
			Method:     http.MethodGet,
			RequestURI: "https://datadoghq.com/",
			Host:       "datadoghq.com",
			RemoteAddr: "1.2.3.4",
			Headers:    map[string][]string{"X-Forwarded": {"127.0.0.1"}, "X-Forwarded-For": {"4.5.6.7", "9.8.7.6"}},
		}
		response = emitter.HandlerOperationRes{
			Headers: map[string][]string{"Content-Type": {"application/json"}, "Content-Length": {"1337"}},
		}
	)

	type testCase struct {
		WAFManager      *config.WAFManager
		ExpectedBlocked bool
		ExpectedTags    map[string]string
	}
	testCases := map[string]testCase{
		"no-supported-addresses": {
			WAFManager: irrelevantRulesWAFManager,
			ExpectedTags: map[string]string{
				"http.client_ip":                       "4.5.6.7",
				"http.request.headers.host":            "datadoghq.com",
				"http.request.headers.x-forwarded-for": "4.5.6.7,9.8.7.6",
				"http.request.headers.x-forwarded":     "127.0.0.1",
				"http.response.headers.content-type":   "application/json",
				"http.response.headers.content-length": "1337",
				"network.client.ip":                    "1.2.3.4",
			},
		},
		"blocking": {
			WAFManager:      blockingRulesWAFManager,
			ExpectedBlocked: true,
			ExpectedTags: map[string]string{
				"appsec.blocked":                       "true",
				"appsec.event":                         "true",
				"http.client_ip":                       "4.5.6.7",
				"http.request.headers.host":            "datadoghq.com",
				"http.request.headers.x-forwarded-for": "4.5.6.7,9.8.7.6",
				"http.request.headers.x-forwarded":     "127.0.0.1",
				"http.response.headers.content-type":   "application/json",
				"http.response.headers.content-length": "1337",
				"network.client.ip":                    "1.2.3.4",
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			rootOp := dyngo.NewRootOperation()
			ctx := dyngo.RegisterOperation(context.Background(), rootOp)

			cfg := &config.Config{
				WAFManager:     tc.WAFManager,
				TraceRateLimit: 1000,
				WAFTimeout:     time.Second,
			}

			waf, err := waf.NewWAFFeature(cfg, rootOp)
			require.NoError(t, err)
			defer waf.Stop()

			feat, err := listener.NewHTTPSecFeature(cfg, rootOp)
			require.NoError(t, err)
			defer feat.Stop()

			blocked := false
			dyngo.OnData(rootOp, func(blk *events.BlockingSecurityEvent) { blocked = blk != nil })

			span := mt.StartSpan("test")
			req, _, _ := emitter.StartOperation(ctx, request, span)
			assert.Equal(t, tc.ExpectedBlocked, blocked)
			req.Finish(response)
			span.Finish()

			finishedSpans := mt.FinishedSpans()
			require.Len(t, finishedSpans, 1)
			finishedSpan := finishedSpans[0]

			assert.Subset(t, finishedSpan.Tags(), tc.ExpectedTags)
		})
	}
}
