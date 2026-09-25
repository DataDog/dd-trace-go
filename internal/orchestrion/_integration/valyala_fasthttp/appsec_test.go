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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	fasthttptrace "github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
)

type appsecTestLogger struct{ *testing.T }

func (l appsecTestLogger) Log(msg string) { l.T.Log(msg) }

func startAppSec(t *testing.T) (tracer.Tracer, agenttest.Agent) {
	t.Helper()
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
	return tr, agent
}

// startServer serves handler without a tracing wrapper, so that only
// Orchestrion can install one.
func startServer(t *testing.T, handler fasthttp.RequestHandler) string {
	t.Helper()
	srv := &fasthttp.Server{Handler: handler}
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
	return "http://" + ln.Addr().String()
}

func TestAppSec(t *testing.T) {
	tr, agent := startAppSec(t)

	var handlerCalls atomic.Int32
	addr := startServer(t, func(ctx *fasthttp.RequestCtx) {
		handlerCalls.Add(1)
		ctx.SetBodyString("handler response")
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
			req, err := http.NewRequest(http.MethodGet, addr+"/"+tc.name, nil)
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

// TestAppSecTimeout checks the order that Orchestrion produces with this
// integration's timeout wrapper: the Orchestrion tracing wrapper outside, and
// the timeout wrapper inside. The worker continues after the deadline.
func TestAppSecTimeout(t *testing.T) {
	tr, agent := startAppSec(t)

	release := make(chan struct{})
	workerDone := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
	// With one worker slot, a later request succeeds only after the timed-out
	// worker has exited and its wrapper has cleaned up.
	addr := startServer(t, fasthttptrace.TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Path()) != "/timeout" {
			ctx.SetBodyString("next response")
			return
		}
		defer close(workerDone)
		<-release
		ctx.SetBodyString("late handler response")
	}, 50*time.Millisecond, "request timed out", fasthttptrace.WithTimeoutConcurrency(1)))
	// Cleanups run in reverse order: release the worker before the server stops.
	t.Cleanup(releaseWorker)

	client := &http.Client{Timeout: 10 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	req, err := http.NewRequest(http.MethodGet, addr+"/timeout", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "<script>alert(1)</script>")
	res, err := client.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusRequestTimeout, res.StatusCode)
	require.Equal(t, "request timed out", string(body))

	// The span and AppSec monitoring end at the deadline, while the worker
	// still runs.
	tr.Flush()
	serverSpan := agenttest.With().
		Operation("http.request").
		Resource("GET /timeout").
		Tag("component", "valyala/fasthttp").
		Tag("span.kind", "server")
	span := agent.RequireSpan(t, serverSpan)
	require.Equal(t, strconv.Itoa(http.StatusRequestTimeout), span.Meta["http.status_code"])
	require.Equal(t, "true", span.Meta["appsec.event"])
	require.Contains(t, span.Meta["_dd.appsec.json"], "crs-941-110")

	releaseWorker()
	select {
	case <-workerDone:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout worker did not finish")
	}
	require.Eventually(t, func() bool {
		res, err := client.Get(addr + "/next")
		if err != nil {
			return false
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode == http.StatusOK
	}, 10*time.Second, 10*time.Millisecond, "the timed-out worker must release its slot")

	tr.Flush()
	agent.RequireSpan(t, agenttest.With().Operation("http.request").Resource("GET /next"))
	require.Nil(t, agent.FindSpan(agenttest.With().
		Operation("http.request").
		Tag("component", "valyala/fasthttp").
		Tag("span.kind", "server").
		Condition("another span for the timed-out request", func(other *agenttest.Span) bool {
			return other.SpanID != span.SpanID && other.TraceID == span.TraceID
		})), "a timed-out request must produce exactly one fasthttp server span")
}
