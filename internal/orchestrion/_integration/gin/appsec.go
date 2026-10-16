// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gin

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAppSec checks that AppSec blocks on the value bound by the
// gin.Context bind methods and on the body passed to the render methods. Form
// bodies are used because the gin contrib does not wrap the form binding, so
// only the bind advice can see them.
type TestCaseAppSec struct {
	*http.Server
	handled atomic.Int32
}

type appsecForm struct {
	Who string `form:"who"`
}

type appsecMessage struct {
	Msg string `json:"msg" xml:"msg" yaml:"msg" toml:"msg"`
}

var appsecBindRoutes = map[string]func(*gin.Context, any) error{
	"/bind":        (*gin.Context).Bind,
	"/should-bind": (*gin.Context).ShouldBind,
}

var appsecRenderRoutes = map[string]func(*gin.Context, int, any){
	"/ascii-json":    (*gin.Context).AsciiJSON,
	"/indented-json": (*gin.Context).IndentedJSON,
	"/json":          (*gin.Context).JSON,
	"/jsonp":         (*gin.Context).JSONP,
	"/pure-json":     (*gin.Context).PureJSON,
	"/secure-json":   (*gin.Context).SecureJSON,
	"/xml":           (*gin.Context).XML,
	"/yaml":          (*gin.Context).YAML,
	"/toml":          (*gin.Context).TOML,
}

func (*TestCaseAppSec) PreBootstrap(_ context.Context, t *testing.T) {
	if ok, err := libddwaf.Usable(); !ok {
		t.Skip("WAF is not available:", err)
	}
	t.Setenv("DD_APPSEC_RULES", "../testdata/gin-appsec.json")
}

func (tc *TestCaseAppSec) Setup(_ context.Context, t *testing.T) {
	gin.SetMode(gin.ReleaseMode) // Silence start-up logging
	engine := gin.New()

	for path, bind := range appsecBindRoutes {
		engine.POST(path, func(c *gin.Context) {
			var form appsecForm
			if err := bind(c, &form); err != nil {
				return
			}
			tc.handled.Add(1)
			c.String(http.StatusOK, "allowed")
		})
	}
	for path, render := range appsecRenderRoutes {
		engine.GET(path, func(c *gin.Context) {
			render(c, http.StatusOK, appsecMessage{Msg: c.Query("msg")})
		})
	}

	ln := net.FreeListener(t)
	tc.Server = &http.Server{
		Addr:    ln.Addr().String(),
		Handler: engine.Handler(),
	}

	go func() { assert.ErrorIs(t, tc.Server.Serve(ln), http.ErrServerClosed) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCaseAppSec) Run(_ context.Context, t *testing.T) {
	for path := range appsecBindRoutes {
		status, body := tc.postForm(t, path, "gin-block-request")
		require.Equal(t, http.StatusForbidden, status, path)
		require.Contains(t, body, "You've been blocked", path)

		status, body = tc.postForm(t, path, "benign")
		require.Equal(t, http.StatusOK, status, path)
		require.Equal(t, "allowed", body, path)
	}
	require.Equal(t, int32(len(appsecBindRoutes)), tc.handled.Load(),
		"blocked requests must not reach the handler past the bind call")

	for path := range appsecRenderRoutes {
		status, body := tc.get(t, path+"?msg=gin-block-response")
		require.Equal(t, http.StatusForbidden, status, path)
		require.Contains(t, body, "You've been blocked", path)
		require.NotContains(t, body, "gin-block-response", path)

		status, body = tc.get(t, path+"?msg=benign")
		require.Equal(t, http.StatusOK, status, path)
		require.Contains(t, body, "benign", path)
	}
}

func (tc *TestCaseAppSec) postForm(t *testing.T, path, who string) (int, string) {
	t.Helper()
	form := url.Values{"who": {who}}
	req, err := http.NewRequest(http.MethodPost, "http://"+tc.Server.Addr+path, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return tc.do(t, req)
}

func (tc *TestCaseAppSec) get(t *testing.T, pathAndQuery string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+tc.Server.Addr+pathAndQuery, nil)
	require.NoError(t, err)
	return tc.do(t, req)
}

func (*TestCaseAppSec) do(t *testing.T, req *http.Request) (int, string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

// ExpectedSpanCount is three spans for each request: net/http client, net/http
// server and gin. Each route gets a blocked and a benign request.
func (*TestCaseAppSec) ExpectedSpanCount() int {
	return 3 * 2 * (len(appsecBindRoutes) + len(appsecRenderRoutes))
}

func (*TestCaseAppSec) ExpectedTraces() trace.Traces {
	var traces trace.Traces
	add := func(method, path, status string, blocked bool) {
		// The blocking tags land on the service entry span, which is the
		// net/http server span.
		server := map[string]string{"component": "net/http", "span.kind": "server", "http.status_code": status}
		if blocked {
			server["appsec.blocked"] = "true"
		}
		traces = append(traces, &trace.Trace{
			Tags: map[string]any{"name": "http.request", "resource": method + " " + path},
			Meta: map[string]string{"component": "net/http", "span.kind": "client", "http.status_code": status},
			Children: trace.Traces{{
				Tags: map[string]any{"name": "http.request", "resource": method + " " + path, "type": "web"},
				Meta: server,
				Children: trace.Traces{{
					Tags: map[string]any{"name": "http.request", "resource": method + " " + path, "service": "gin.router"},
					Meta: map[string]string{"component": "gin-gonic/gin", "span.kind": "server", "http.status_code": status},
				}},
			}},
		})
	}
	for path := range appsecBindRoutes {
		add(http.MethodPost, path, "403", true)
		add(http.MethodPost, path, "200", false)
	}
	for path := range appsecRenderRoutes {
		add(http.MethodGet, path, "403", true)
		add(http.MethodGet, path, "200", false)
	}
	return traces
}
