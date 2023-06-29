// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package echo

import (
	"errors"
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

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestAppSec(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	e := echo.New()
	e.Use(Middleware())

	// Add some testing routes
	e.POST("/path0.0/:myPathParam0/path0.1/:myPathParam1/path0.2/:myPathParam2/path0.3/*myPathParam3", func(c echo.Context) error {
		return c.String(200, "Hello World!\n")
	})
	e.POST("/", func(c echo.Context) error {
		return c.String(200, "Hello World!\n")
	})
	e.POST("/body", func(c echo.Context) error {
		pappsec.MonitorParsedHTTPBody(c.Request().Context(), "$globals")
		return c.String(200, "Hello Body!\n")
	})

	e.Any("/error", func(_ echo.Context) error {
		return errors.New("what status code will I yield")
	})
	e.Any("/nil", func(_ echo.Context) error {
		return nil
	})

	srv := httptest.NewServer(e)
	defer srv.Close()

	// Test an LFI attack via path parameters
	t.Run("request-uri", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		// Send an LFI attack (according to appsec rule id crs-930-110)
		req, err := http.NewRequest("POST", srv.URL+"/../../../secret.txt", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		// Check that the server behaved as intended (no 301 but 404 directly)
		require.Equal(t, http.StatusNotFound, res.StatusCode)
		// The span should contain the security event
		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "crs-930-110"))
		require.True(t, strings.Contains(event, "server.request.uri.raw"))
	})

	// Test a security scanner attack via path parameters
	t.Run("path-params", func(t *testing.T) {
		t.Run("regular", func(t *testing.T) {
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
			require.Equal(t, "Hello World!\n", string(b))
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

		t.Run("wildcard", func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			// Send a security scanner attack (according to appsec rule id crs-913-120)
			req, err := http.NewRequest("POST", srv.URL+"/path0.0/param0/path0.1/param1/path0.2/param2/path0.3/appscan_fingerprint", nil)
			if err != nil {
				panic(err)
			}
			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			// Check that the handler was properly called
			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, "Hello World!\n", string(b))
			require.Equal(t, http.StatusOK, res.StatusCode)
			// The span should contain the security event
			finished := mt.FinishedSpans()
			require.Len(t, finished, 1)
			event := finished[0].Tag("_dd.appsec.json").(string)
			require.NotNil(t, event)
			require.True(t, strings.Contains(event, "crs-913-120"))
			// Wildcards are not named in echo
			require.False(t, strings.Contains(event, "myPathParam3"))
			require.True(t, strings.Contains(event, "server.request.path_params"))
		})
	})

	t.Run("response-status", func(t *testing.T) {
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

	for _, tc := range []struct {
		name     string
		endpoint string
		status   int
		headers  map[string]string
	}{
		{
			name:     "nil",
			endpoint: "/nil",
			status:   http.StatusOK,
		},
		{
			name:     "nil-with-attack",
			endpoint: "/nil",
			headers:  map[string]string{"user-agent": "arachni/v1"},
			status:   http.StatusOK,
		},
		{
			name:     "custom-error",
			endpoint: "/error",
			status:   http.StatusInternalServerError,
		},
		{
			name:     "custom-error-with-attack",
			endpoint: "/error",
			headers:  map[string]string{"user-agent": "arachni/v1"},
			status:   http.StatusInternalServerError,
		},
	} {
		t.Run("error-handler/"+tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			req, err := http.NewRequest("POST", srv.URL+tc.endpoint, nil)
			require.NoError(t, err)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			require.Equal(t, tc.status, res.StatusCode)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Equal(t, spans[0].Tag("http.status_code"), fmt.Sprintf("%d", tc.status))
			// Just make sure an attack was detected in case we meant for one to happen. We don't care what the attack
			// is, just that the status code reporting behaviour is accurate
			if tc.headers != nil {
				require.Contains(t, spans[0].Tags(), "_dd.appsec.json")
			}
		})
	}
}

// TestControlFlow ensures that the AppSec middleware behaves correctly in various execution flows and wrapping
// scenarios.
func TestControlFlow(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("AppSec needs to be enabled for this test")
	}

	middlewareResponseBody := "Hello Middleware"
	middlewareResponseStatus := 433
	handlerResponseBody := "Hello Handler"
	handlerResponseStatus := 533

	for _, tc := range []struct {
		name        string
		middlewares []echo.MiddlewareFunc
		handler     func(echo.Context) error
		test        func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer, err error)
	}{
		{
			// In this case the middleware we wrap aborts before the handler gets called.
			// We must check that the status code we retrieve is the one set by the middleware when erroring out.
			name: "middleware-first/middleware-aborts-before-handler",
			middlewares: []echo.MiddlewareFunc{
				Middleware(),
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						c.String(middlewareResponseStatus, middlewareResponseBody)
						return echo.NewHTTPError(middlewareResponseStatus, "middleware abort")
					}
				},
			},
			handler: func(echo.Context) error {
				panic("unexpected control flow")
				return nil
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer, err error) {
				require.Error(t, err)
				e := err.(*echo.HTTPError)
				require.Equal(t, "middleware abort", e.Message)
				require.Equal(t, middlewareResponseStatus, e.Code)
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())

				spans := mt.FinishedSpans()
				require.Equal(t, 1, len(spans))
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
		{
			// In this case the handler errors out.
			// We check that the status code read is the one set by said handler.
			name: "middleware-first/handler-aborts",
			middlewares: []echo.MiddlewareFunc{
				Middleware(),
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := next(c)
						require.Error(t, err)
						return err
					}
				},
			},
			handler: func(c echo.Context) error {
				c.String(handlerResponseStatus, handlerResponseBody)
				return echo.NewHTTPError(handlerResponseStatus, "handler abort")
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer, err error) {
				require.Error(t, err)
				e := err.(*echo.HTTPError)
				require.Equal(t, "handler abort", e.Message)
				require.Equal(t, handlerResponseStatus, e.Code)
				require.Equal(t, handlerResponseStatus, rec.Code)
				require.Equal(t, handlerResponseBody, rec.Body.String())

				spans := mt.FinishedSpans()
				require.Equal(t, 1, len(spans))
				status := spans[0].Tag("http.status_code").(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
		{
			// In this case no errors occur, and we check that the retrieved status code is correct.
			name: "middleware-first/no-aborts",
			middlewares: []echo.MiddlewareFunc{
				Middleware(),
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						err = next(c)
						require.NoError(t, err)
						err = c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						return err
					}
				},
			},
			handler: func(c echo.Context) error {
				return c.String(handlerResponseStatus, handlerResponseBody)
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer, err error) {
				require.NoError(t, err)
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody+handlerResponseBody+middlewareResponseBody, rec.Body.String())

				spans := mt.FinishedSpans()
				require.Equal(t, 1, len(spans))
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
		{
			// In this case the middleware we wrap errors out after calling the handler.
			// We check that the status code we read is the one set by the middleware when erroring out.
			name: "middleware-first/middleware-aborts-after-handler",
			middlewares: []echo.MiddlewareFunc{
				Middleware(),
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := next(c)
						require.NoError(t, err)
						err = c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						return echo.NewHTTPError(middlewareResponseStatus, "middleware abort")
					}
				},
			},
			handler: func(c echo.Context) error {
				return nil
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer, err error) {
				require.Error(t, err)
				e := err.(*echo.HTTPError)
				require.Equal(t, "middleware abort", e.Message)
				require.Equal(t, middlewareResponseStatus, e.Code)
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())

				spans := mt.FinishedSpans()
				require.Equal(t, 1, len(spans))
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
		{
			// This is the corner case where another middleware wraps ours meaning we can't control the status code
			// because it can be overwritten after our middleware.
			name: "middleware-after/middleware-aborts-after-next-handler",
			middlewares: []echo.MiddlewareFunc{
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := next(c)
						require.NoError(t, err)
						err = c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						return echo.NewHTTPError(middlewareResponseStatus, "middleware abort")
					}
				},
				Middleware(),
			},
			handler: func(c echo.Context) error {
				// Do nothing so that the calling middleware can handle the response.
				return nil
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer, err error) {
				require.Error(t, err)
				e := err.(*echo.HTTPError)
				require.Equal(t, "middleware abort", e.Message)
				require.Equal(t, middlewareResponseStatus, e.Code)
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())

				spans := mt.FinishedSpans()
				require.Equal(t, 1, len(spans))
			},
		},
		{
			// In this case the middleware that wraps ours errors out before calling it.
			// Check that no span is generated.
			name: "middleware-after/middleware-aborts-before-next-handler",
			middlewares: []echo.MiddlewareFunc{
				func(echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						return echo.NewHTTPError(middlewareResponseStatus, "middleware abort")
					}
				},
				func(echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						// Make sure echo doesn't call the next middleware when the
						// previous one returns an error.
						panic("unexpected control flow")
					}
				},
				Middleware(),
			},
			handler: func(echo.Context) error {
				// Make sure echo doesn't call the handler when one of the
				// previous middlewares return an error.
				panic("unexpected control flow")
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer, err error) {
				require.Error(t, err)
				e := err.(*echo.HTTPError)
				require.Equal(t, "middleware abort", e.Message)
				require.Equal(t, middlewareResponseStatus, e.Code)
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())

				// The middleware doesn't get executed, no span expected
				spans := mt.FinishedSpans()
				require.Equal(t, 0, len(spans))
			},
		},
		{
			// This is a special case where our middleware is wrapped but the error happens in the
			// handler and the status code doesn't change past our execution.
			name: "middleware-after/handler-aborts",
			middlewares: []echo.MiddlewareFunc{
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := next(c)
						require.Error(t, err)
						return err
					}
				},
				Middleware(),
			},
			handler: func(c echo.Context) error {
				err := c.String(handlerResponseStatus, handlerResponseBody)
				require.NoError(t, err)
				return echo.NewHTTPError(handlerResponseStatus, "handler abort")
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer, err error) {
				require.Error(t, err)
				e := err.(*echo.HTTPError)
				require.Equal(t, "handler abort", e.Message)
				require.Equal(t, handlerResponseStatus, e.Code)
				require.Equal(t, handlerResponseStatus, rec.Code)
				require.Equal(t, handlerResponseBody, rec.Body.String())

				spans := mt.FinishedSpans()
				require.Equal(t, 1, len(spans))
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
		{
			// This is a special case where our middleware is wrapped but no error occurs
			// and the status code doesn't change past our execution.
			name: "middleware-after/no-aborts",
			middlewares: []echo.MiddlewareFunc{
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						err = next(c)
						require.NoError(t, err)
						err = c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						return nil
					}
				},
				Middleware(),
			},
			handler: func(c echo.Context) error {
				err := c.String(handlerResponseStatus, handlerResponseBody)
				require.NoError(t, err)
				return nil
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, mt mocktracer.Tracer, err error) {
				require.NoError(t, err)
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody+handlerResponseBody+middlewareResponseBody, rec.Body.String())

				spans := mt.FinishedSpans()
				require.Equal(t, 1, len(spans))
				status := spans[0].Tag(ext.HTTPCode).(string)
				require.Equal(t, status, fmt.Sprint(rec.Code))
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			// Create a echo router
			router := echo.New()
			// Setup the middleware
			router.Use(tc.middlewares...)
			// Add the endpoint
			router.GET("/", tc.handler)

			// Perform the request and record the output
			rec := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/", nil)
			var err error
			router.HTTPErrorHandler = func(e error, _ echo.Context) {
				err = e
			}
			router.ServeHTTP(rec, req)
			// Check the request was performed as expected
			tc.test(t, rec, mt, err)
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

	// Start and trace an HTTP server
	e := echo.New()
	e.Use(Middleware())
	e.Any("/ip", func(c echo.Context) error {
		return c.String(http.StatusOK, "Hello World!\n")
	})
	e.Any("/user", func(c echo.Context) error {
		userID := c.Request().Header.Get("user-id")
		if err := pappsec.SetUser(c.Request().Context(), userID); err != nil {
			return err
		}
		return c.String(http.StatusOK, "Hello, "+userID)
	})
	srv := httptest.NewServer(e)
	defer srv.Close()

	for _, tc := range []struct {
		name        string
		endpoint    string
		headers     map[string]string
		shouldBlock bool
	}{
		{
			name:        "ip/block",
			endpoint:    "/ip",
			headers:     map[string]string{"x-forwarded-for": "1.2.3.4"},
			shouldBlock: true,
		},
		{
			name:     "ip/no-block",
			endpoint: "/ip",
			headers:  map[string]string{"x-forwarded-for": "1.2.3.5"},
		},
		{
			name:        "user/block",
			endpoint:    "/user",
			headers:     map[string]string{"user-id": "blocked-user-1"},
			shouldBlock: true,
		},
		{
			name:     "user/no-block",
			endpoint: "/user",
			headers:  map[string]string{"user-id": "legit-user-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			req, err := http.NewRequest("POST", srv.URL+tc.endpoint, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			require.NoError(t, err)
			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			if tc.shouldBlock {
				require.Equal(t, http.StatusForbidden, res.StatusCode)
				require.Equal(t, spans[0].Tag("appsec.blocked"), true)
			} else {
				require.Equal(t, http.StatusOK, res.StatusCode)
				require.NotContains(t, spans[0].Tags(), "appsec.blocked")
			}
			require.Equal(t, spans[0].Tag("http.status_code"), fmt.Sprintf("%d", res.StatusCode))

		})
	}
}
