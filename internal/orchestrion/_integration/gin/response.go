// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gin

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestCaseResponse struct {
	*http.Server
}

type Payload struct {
	Who string `json:"hello" xml:"who,attr"`
}

func (tc *TestCaseResponse) Setup(_ context.Context, t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("AAP features are not supported on Windows")
		return
	}

	gin.SetMode(gin.ReleaseMode) // Silence start-up logging
	engine := gin.New()

	engine.POST("/json", func(c *gin.Context) {
		var payload Payload
		if err := c.Bind(&payload); err != nil {
			t.Log(err)
			c.Error(err)
			return
		}
		c.JSON(http.StatusOK, payload)
	})

	engine.POST("/xml", func(c *gin.Context) {
		var payload Payload
		if err := c.Bind(&payload); err != nil {
			t.Log(err)
			c.Error(err)
			return
		}
		c.XML(http.StatusOK, struct {
			XMLName xml.Name `json:"-" xml:"hello"`
			Payload
		}{Payload: payload})
	})

	tc.Server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", net.FreePort(t)),
		Handler: engine.Handler(),
	}

	go func() { assert.ErrorIs(t, tc.Server.ListenAndServe(), http.ErrServerClosed) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCaseResponse) Run(_ context.Context, t *testing.T) {
	requestBody := `{"hello":"world"}`
	req, err := http.NewRequest("POST", "http://"+tc.Server.Addr+"/json", strings.NewReader(requestBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, requestBody, string(body))

	req, err = http.NewRequest("POST", "http://"+tc.Server.Addr+"/xml", strings.NewReader(requestBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, `<hello who="world"></hello>`, string(body))
}

func (tc *TestCaseResponse) ExpectedTraces() trace.Traces {
	return trace.Traces{
		// The JSON endpoint
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "POST /json",
				"service":  "gin.test",
				"type":     "http",
			},
			Meta: map[string]string{
				"http.url":  "http://" + tc.Server.Addr + "/json",
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "POST /json",
						"service":  "gin.test",
						"type":     "web",
					},
					Meta: map[string]string{
						"http.url":  "http://" + tc.Server.Addr + "/json",
						"component": "net/http",
						"span.kind": "server",
						// Verify we collected the schemas (proving the WAF has seen them)
						//TODO: "_dd.appsec.s.req.body": `[{"hello":[8]}]`,
						"_dd.appsec.s.res.body": `[{"hello":[8]}]`,
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "POST /json",
								"service":  "gin.router",
								"type":     "web",
							},
							Meta: map[string]string{
								"http.url":  "http://" + tc.Server.Addr + "/json",
								"component": "gin-gonic/gin",
								"span.kind": "server",
							},
						},
					},
				},
			},
		},
		// The XML endpoint
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "POST /xml",
				"service":  "gin.test",
				"type":     "http",
			},
			Meta: map[string]string{
				"http.url":  "http://" + tc.Server.Addr + "/xml",
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "POST /xml",
						"service":  "http.router",
						"type":     "web",
					},
					Meta: map[string]string{
						"http.url":  "http://" + tc.Server.Addr + "/xml",
						"component": "net/http",
						"span.kind": "server",
						// Verify we collected the schemas (proving the WAF has seen them)
						//TODO: "_dd.appsec.s.req.body": `[{"hello":[8]}]`,
						"_dd.appsec.s.res.body": `[{"Payload":[{"hello":[8]}]}]`,
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "POST /xml",
								"service":  "gin.router",
								"type":     "web",
							},
							Meta: map[string]string{
								"http.url":  "http://" + tc.Server.Addr + "/xml",
								"component": "gin-gonic/gin",
								"span.kind": "server",
							},
						},
					},
				},
			},
		},
	}
}
