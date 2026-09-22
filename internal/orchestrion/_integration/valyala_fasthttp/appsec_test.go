// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fasthttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
)

type appsecTestLogger struct{ *testing.T }

func (l appsecTestLogger) Log(msg string) { l.T.Log(msg) }

func TestAppSec(t *testing.T) {
	require.True(t, built.WithOrchestrion, "this test must be run with orchestrion enabled")
	if runtime.GOOS == "windows" {
		t.Skip("appsec does not support Windows")
	}
	if ok, err := libddwaf.Usable(); !ok {
		t.Skip("WAF is not available:", err)
	}
	t.Setenv("DD_APPSEC_RULES", "../../../appsec/testdata/blocking.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	t.Setenv("DD_API_SECURITY_SAMPLE_DELAY", "0")
	// The shared harness leaves its logger bound to the previous test.
	t.Cleanup(log.UseLogger(appsecTestLogger{t}))

	tr, agent, err := tracertest.Bootstrap(t,
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
		tracer.WithAppSecEnabled(true),
	)
	require.NoError(t, err)

	var handlerCalls atomic.Int32
	// Leave the handler unwrapped: only Orchestrion should install AppSec.
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		handlerCalls.Add(1)
		ctx.SetBodyString("handler response")
	}}
	ln := net.FreeListener(t)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, srv.ShutdownWithContext(ctx))
		select {
		case err := <-serveErr:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal("server did not stop:", ctx.Err())
		}
	})

	client := &http.Client{Timeout: 10 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	for _, tc := range []struct {
		name         string
		headers      map[string]string
		status       int
		rule         string
		handlerCalls int32
	}{
		{
			name:         "clean",
			status:       http.StatusOK,
			handlerCalls: 1,
		},
		{
			name:         "detect",
			headers:      map[string]string{"User-Agent": "<script>alert(1)</script>"},
			status:       http.StatusOK,
			rule:         "crs-941-110",
			handlerCalls: 1,
		},
		{
			name:    "block",
			headers: map[string]string{"X-Forwarded-For": "1.2.3.4", "Accept": "application/json"},
			status:  http.StatusForbidden,
			rule:    "blk-001-001",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handlerCalls.Store(0)
			req, err := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/"+tc.name, nil)
			require.NoError(t, err)
			for key, value := range tc.headers {
				req.Header.Set(key, value)
			}
			res, err := client.Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, tc.status, res.StatusCode)
			require.Equal(t, tc.handlerCalls, handlerCalls.Load())
			if tc.status == http.StatusForbidden {
				require.Equal(t, "application/json", res.Header.Get("Content-Type"))
				require.True(t, json.Valid(body), "blocking response must be valid JSON: %s", body)
				require.NotContains(t, string(body), "handler response")
			} else {
				require.Equal(t, "handler response", string(body))
			}

			tr.Flush()
			serverSpan := agenttest.With().
				Operation("http.request").
				Resource("GET /"+tc.name).
				Tag("component", "valyala/fasthttp").
				Tag("span.kind", "server")
			span := agent.RequireSpan(t, serverSpan)
			require.Nil(t, agent.FindSpan(serverSpan.Condition("another server span", func(other *agenttest.Span) bool {
				return other.SpanID != span.SpanID
			})), "each request must produce exactly one fasthttp server span")
			require.Equal(t, strconv.Itoa(tc.status), span.Meta["http.status_code"])
			if tc.rule == "" {
				require.NotContains(t, span.Meta, "_dd.appsec.json")
				require.NotContains(t, span.Meta, "appsec.event")
			} else {
				require.Equal(t, "true", span.Meta["appsec.event"])
				var event struct {
					Triggers []struct {
						Rule struct{ ID string }
					}
				}
				require.NoError(t, json.Unmarshal([]byte(span.Meta["_dd.appsec.json"]), &event))
				ruleIDs := make([]string, 0, len(event.Triggers))
				for _, trigger := range event.Triggers {
					ruleIDs = append(ruleIDs, trigger.Rule.ID)
				}
				require.Contains(t, ruleIDs, tc.rule)
			}
			if tc.status == http.StatusForbidden {
				require.Equal(t, "true", span.Meta["appsec.blocked"])
			} else {
				require.NotContains(t, span.Meta, "appsec.blocked")
			}
		})
	}
}
