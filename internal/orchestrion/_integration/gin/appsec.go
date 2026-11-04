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
	"github.com/gin-gonic/gin/binding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	gintrace "github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAppSec checks that AppSec blocks on the value bound by the
// gin.Context bind methods and on the body passed to the render methods. The
// gin contrib also monitors the JSON, XML, YAML and TOML bindings itself, so
// the test swaps those back to gin's own while it runs. Only the bind advice
// can then see the bound value, and a bind rule that matches nothing fails.
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

type appsecBindRoute struct {
	bind        func(*gin.Context, any) error
	contentType string
	body        func(msg string) string
	// form binds into appsecForm, the others into appsecMessage.
	form bool
}

func formBody(msg string) string { return url.Values{"who": {msg}}.Encode() }
func jsonBody(msg string) string { return `{"msg":"` + msg + `"}` }
func tomlBody(msg string) string { return `msg = "` + msg + `"` }
func yamlBody(msg string) string { return "msg: " + msg }
func xmlBody(msg string) string  { return "<appsecMessage><msg>" + msg + "</msg></appsecMessage>" }

var appsecBindRoutes = map[string]appsecBindRoute{
	"/bind":             {(*gin.Context).Bind, "application/x-www-form-urlencoded", formBody, true},
	"/should-bind":      {(*gin.Context).ShouldBind, "application/x-www-form-urlencoded", formBody, true},
	"/bind-json":        {(*gin.Context).BindJSON, "application/json", jsonBody, false},
	"/should-bind-json": {(*gin.Context).ShouldBindJSON, "application/json", jsonBody, false},
	"/bind-toml":        {(*gin.Context).BindTOML, "application/toml", tomlBody, false},
	"/should-bind-toml": {(*gin.Context).ShouldBindTOML, "application/toml", tomlBody, false},
	"/bind-yaml":        {(*gin.Context).BindYAML, "application/x-yaml", yamlBody, false},
	"/should-bind-yaml": {(*gin.Context).ShouldBindYAML, "application/x-yaml", yamlBody, false},
	"/bind-xml":         {(*gin.Context).BindXML, "application/xml", xmlBody, false},
	"/should-bind-xml":  {(*gin.Context).ShouldBindXML, "application/xml", xmlBody, false},
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
	"/protobuf": func(c *gin.Context, code int, obj any) {
		c.ProtoBuf(code, wrapperspb.String(obj.(appsecMessage).Msg))
	},
}

func (*TestCaseAppSec) PreBootstrap(_ context.Context, t *testing.T) {
	if ok, err := libddwaf.Usable(); !ok {
		t.Skip("WAF is not available:", err)
	}
	t.Setenv("DD_APPSEC_RULES", "../testdata/gin-appsec.json")
}

func (tc *TestCaseAppSec) Setup(_ context.Context, t *testing.T) {
	gin.SetMode(gin.ReleaseMode) // Silence start-up logging
	unwrapAppSecBinding(t, &binding.JSON)
	unwrapAppSecBinding(t, &binding.XML)
	unwrapAppSecBinding(t, &binding.YAML)
	unwrapAppSecBinding(t, &binding.TOML)
	engine := gin.New()

	for path, route := range appsecBindRoutes {
		engine.POST(path, func(c *gin.Context) {
			var target any = &appsecMessage{}
			if route.form {
				target = &appsecForm{}
			}
			if err := route.bind(c, target); err != nil {
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
	for path, route := range appsecBindRoutes {
		status, body := tc.post(t, path, route, "gin-block-request")
		require.Equal(t, http.StatusForbidden, status, path)
		require.Contains(t, body, "You've been blocked", path)

		status, body = tc.post(t, path, route, "benign")
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

// unwrapAppSecBinding replaces the gin contrib's AppSec wrapper in *b with the
// binding it wraps, until the test ends.
func unwrapAppSecBinding(t *testing.T, b *binding.BindingBody) {
	wrapped, ok := (*b).(gintrace.AppsecBinding)
	require.True(t, ok, "the gin contrib must wrap %T", *b)
	*b = wrapped.BindingBody
	t.Cleanup(func() { *b = wrapped })
}

func (tc *TestCaseAppSec) post(t *testing.T, path string, route appsecBindRoute, msg string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+tc.Server.Addr+path, strings.NewReader(route.body(msg)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", route.contentType)
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
