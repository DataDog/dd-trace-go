// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package gin

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pappsec "gopkg.in/DataDog/dd-trace-go.v1/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAppSec(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	r := gin.New()
	r.Use(Middleware("appsec"))
	r.Any("/lfi/*allPaths", func(c *gin.Context) {
		c.String(200, "Hello World!\n")
	})
	r.Any("/path0.0/:myPathParam0/path0.1/:myPathParam1/path0.2/:myPathParam2/path0.3/*param3", func(c *gin.Context) {
		c.String(200, "Hello Params!\n")
	})
	r.Any("/body", func(c *gin.Context) {
		pappsec.MonitorParsedHTTPBody(c.Request.Context(), "$globals")
		c.String(200, "Hello Body!\n")
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	t.Run("request-uri", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		// Send an LFI attack (according to appsec rule id crs-930-110)
		req, err := http.NewRequest("POST", srv.URL+"/lfi/../../../secret.txt", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		// Check that the server behaved as intended
		require.Equal(t, http.StatusOK, res.StatusCode)
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(b))
		// The span should contain the security event
		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// The first 301 redirection should contain the attack via the request uri
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "server.request.uri.raw"))
		require.True(t, strings.Contains(event, "crs-930-110"))
	})

	// Test a security scanner attack via path parameters
	t.Run("path-params", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		// Send a security scanner attack (according to appsec rule id crs-913-120)
		req, err := http.NewRequest("POST", srv.URL+"/path0.0/param0/path0.1/param1/path0.2/appscan_fingerprint/path0.3/param3", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		// Check that the handler was properly called
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello Params!\n", string(b))
		require.Equal(t, http.StatusOK, res.StatusCode)
		// The span should contain the security event
		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "crs-913-120"))
		require.True(t, strings.Contains(event, "myPathParam2"))
		require.True(t, strings.Contains(event, "server.request.path_params"))
	})

	t.Run("status-code", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srv.URL+"/etc/", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, 404, res.StatusCode)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "server.response.status"))
		require.True(t, strings.Contains(event, "nfd-000-001"))

	})

	// Test a PHP injection attack via request parsed body
	t.Run("SDK-body", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srv.URL+"/body", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		// Check that the handler was properly called
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello Body!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.True(t, strings.Contains(event.(string), "crs-933-130"))
	})
}

func TestControlFlow(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()
	middlewareResponseBody := "Hello Middleware"
	middlewareResponseStatus := 433
	handlerResponseBody := "Hello Handler"
	handlerResponseStatus := 533

	for _, tc := range []struct {
		name        string
		middlewares []gin.HandlerFunc
		handler     func(*gin.Context)
		test        func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer)
	}{
		{
			name: "middleware-first/middleware-aborts-before-handler",
			middlewares: []gin.HandlerFunc{
				Middleware("appsec"),
				func(c *gin.Context) {
					c.String(middlewareResponseStatus, middlewareResponseBody)
					c.Abort()
				},
			},
			handler: func(*gin.Context) {
				panic("unexpected control flow")
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer) {
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
		{
			name: "middleware-first/handler-aborts",
			middlewares: []gin.HandlerFunc{
				Middleware("appsec"),
				func(c *gin.Context) {
					c.Next()
					if !c.IsAborted() {
						panic("unexpected flow")
					}
				},
			},
			handler: func(c *gin.Context) {
				c.String(handlerResponseStatus, handlerResponseBody)
				c.Abort()
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer) {
				require.Equal(t, handlerResponseStatus, rec.Code)
				require.Equal(t, handlerResponseBody, rec.Body.String())
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
		{
			name: "middleware-first/no-aborts",
			middlewares: []gin.HandlerFunc{
				Middleware("appsec"),
				func(c *gin.Context) {
					c.String(middlewareResponseStatus, middlewareResponseBody)
					c.Next()
					c.String(middlewareResponseStatus, middlewareResponseBody)
				},
			},
			handler: func(c *gin.Context) {
				c.String(handlerResponseStatus, handlerResponseBody)
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer) {
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody+handlerResponseBody+middlewareResponseBody, rec.Body.String())
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
		{
			name: "middleware-first/middleware-aborts-after-handler",
			middlewares: []gin.HandlerFunc{
				Middleware("appsec"),
				func(c *gin.Context) {
					c.Next()
					c.String(middlewareResponseStatus, middlewareResponseBody)
					c.Abort()
				},
			},
			handler: func(c *gin.Context) {
				// Do nothing so that the calling middleware can handle the response.
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer) {
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
		{
			name: "middleware-after/middleware-aborts-before-next-handler",
			middlewares: []gin.HandlerFunc{
				func(c *gin.Context) {
					c.String(middlewareResponseStatus, middlewareResponseBody)
					c.Abort()
				},
				func(*gin.Context) {
					// Make sure gin doesn't call the next middleware when the previous
					// one aborts.
					panic("unexpected control flow")
				},
				Middleware("appsec"),
			},
			handler: func(*gin.Context) {
				panic("unexpected control flow")
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer) {
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())
				spans := mt.FinishedSpans()
				require.Empty(t, spans)
			},
		},
		{
			name: "middleware-after/no-aborts",
			middlewares: []gin.HandlerFunc{
				func(c *gin.Context) {
					c.String(middlewareResponseStatus, middlewareResponseBody)
					c.Next()
				},
				Middleware("appsec"),
			},
			handler: func(c *gin.Context) {
				c.String(handlerResponseStatus, handlerResponseBody)
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer) {
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody+handlerResponseBody, rec.Body.String())
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, fmt.Sprint(middlewareResponseStatus), status)
			},
		},
		{
			name: "middleware-after/handler-aborts",
			middlewares: []gin.HandlerFunc{
				func(c *gin.Context) {
					c.Next()
					if !c.IsAborted() {
						panic("unexpected control flow")
					}
				},
				Middleware("appsec"),
			},
			handler: func(c *gin.Context) {
				c.String(handlerResponseStatus, handlerResponseBody)
				c.Abort()
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer) {
				require.Equal(t, handlerResponseStatus, rec.Code)
				require.Equal(t, handlerResponseBody, rec.Body.String())
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
		{
			name: "middleware-after/middleware-aborts-after-next-handler",
			middlewares: []gin.HandlerFunc{
				func(c *gin.Context) {
					c.Next()
					if !c.IsAborted() {
						panic("unexpected control flow")
					}
				},
				func(c *gin.Context) {
					c.Next()
					c.String(middlewareResponseStatus, middlewareResponseBody)
					c.Abort()
				},
				Middleware("appsec"),
			},
			handler: func(*gin.Context) {
				// Do nothing so that the calling middleware can handle the response.
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer) {
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				// Status code is set after AppSec middleware meaning we can't assert anything else
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			// Create a Gin router
			router := gin.New()
			// Setup the middleware
			router.Use(tc.middlewares...)
			// Add the endpoint
			router.GET("/", tc.handler)

			// Perform the request and record the output
			rec := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/", nil)
			router.ServeHTTP(rec, req)

			// Check the request was performed as expected
			tc.test(t, rec, mt)
		})
	}
}

// Test that IP blocking works by using custom rules/rules data
func TestBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")

	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("AppSec needs to be enabled for this test")
	}

	r := gin.New()
	r.Use(Middleware("appsec"))
	r.Any("/", func(c *gin.Context) {
		c.String(200, "Hello World!\n")
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	t.Run("block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srv.URL, nil)
		if err != nil {
			panic(err)
		}
		// Hardcoded IP header holding an IP that is blocked
		req.Header.Set("x-forwarded-for", "1.2.3.4")
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		// Check that the request was blocked
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NotContains(t, string(b), "Hello World!\n")
		require.Equal(t, 403, res.StatusCode)
	})

	t.Run("no-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req1, err := http.NewRequest("POST", srv.URL, nil)
		if err != nil {
			panic(err)
		}
		req2, err := http.NewRequest("POST", srv.URL, nil)
		if err != nil {
			panic(err)
		}
		req2.Header.Set("x-forwarded-for", "1.2.3.5")

		for _, r := range []*http.Request{req1, req2} {
			res, err := srv.Client().Do(r)
			require.NoError(t, err)
			defer res.Body.Close()
			// Check that the request was not blocked
			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, "Hello World!\n", string(b))

		}
	})
}
