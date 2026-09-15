// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	router := echo.New()
	router.Use(Middleware(WithService("foobar")))
	router.GET("/user/:id", func(c echo.Context) error {
		called = true
		_, traced = tracer.SpanFromContext(c.Request().Context())
		return c.NoContent(200)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	// verify traces look good
	assert.True(called)
	assert.True(traced)
}

func TestWithHeaderTags(t *testing.T) {
	setupReq := func(opts ...Option) *http.Request {
		router := echo.New()
		router.Use(Middleware(opts...))

		router.GET("/test", func(c echo.Context) error {
			return c.String(http.StatusOK, "test")
		})
		r := httptest.NewRequest("GET", "/test", nil)
		r.Header.Set("h!e@a-d.e*r", "val")
		r.Header.Add("h!e@a-d.e*r", "val2")
		r.Header.Set("2header", "2val")
		r.Header.Set("3header", "3val")
		r.Header.Set("x-datadog-header", "value")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return r
	}
	t.Run("default-off", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		htArgs := []string{"h!e@a-d.e*r", "2header", "3header", "x-datadog-header"}
		setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]
		instrumentation.NewHeaderTags(htArgs).Iter(func(_ string, tag string) {
			assert.NotContains(s.Tags(), tag)
		})
	})
	t.Run("integration", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		htArgs := []string{"h!e@a-d.e*r", "2header:tag"}
		_ = setupReq(WithHeaderTags(htArgs))
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		assert.Equal("val,val2", s.Tags()["http.request.headers.h_e_a-d_e_r"])
		assert.Equal("2val", s.Tags()["tag"])
		assert.NotContains(s.Tags(), "http.headers.x-datadog-header")
	})

	t.Run("global", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalHeaderTags(t, "3header")

		_ = setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		assert.Equal("3val", s.Tags()["http.request.headers.3header"])
		assert.NotContains(s.Tags(), "http.request.headers.other")
		assert.NotContains(s.Tags(), "http.headers.x-datadog-header")
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalHeaderTags(t, "3header")

		htArgs := []string{"h!e@a-d.e*r", "2header:tag"}
		_ = setupReq(WithHeaderTags(htArgs))
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		assert.Equal("val,val2", s.Tags()["http.request.headers.h_e_a-d_e_r"])
		assert.Equal("2val", s.Tags()["tag"])
		assert.NotContains(s.Tags(), "http.headers.x-datadog-header")
		assert.NotContains(s.Tags(), "http.request.headers.3header")
	})
}

func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	router := echo.New()
	router.Use(Middleware(WithService("foobar"), WithAnalytics(false)))
	router.GET("/user/:id", func(c echo.Context) error {
		called = true
		var span *tracer.Span
		span, traced = tracer.SpanFromContext(c.Request().Context())
		ms := mocktracer.MockSpan(span)

		// we patch the span on the request context.
		span.SetTag("test.echo", "echony")
		assert.Equal(ms.Tag(ext.ServiceName), "foobar")
		return c.NoContent(200)
	})

	root := tracer.StartSpan("root")
	r := httptest.NewRequest("GET", "/user/123", nil)
	err := tracer.Inject(root.Context(), tracer.HTTPHeadersCarrier(r.Header))
	assert.Nil(err)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	// verify traces look good
	assert.True(called)
	assert.True(traced)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("foobar", span.Tag(ext.ServiceName))
	assert.Equal("echony", span.Tag("test.echo"))
	assert.Contains(span.Tag(ext.ResourceName), "/user/:id")
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal(root.Context().SpanID(), span.ParentID())
	assert.Equal("labstack/echo.v4", span.Tag(ext.Component))
	assert.Equal(string(instrumentation.PackageLabstackEchoV4), span.Integration())
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))

	assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
}

func TestTraceAnalytics(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	router := echo.New()
	router.Use(Middleware(WithService("foobar"), WithAnalytics(true)))
	router.GET("/user/:id", func(c echo.Context) error {
		called = true
		var span *tracer.Span
		span, traced = tracer.SpanFromContext(c.Request().Context())
		ms := mocktracer.MockSpan(span)

		// we patch the span on the request context.
		span.SetTag("test.echo", "echony")
		assert.Equal(ms.Tag(ext.ServiceName), "foobar")
		return c.NoContent(200)
	})

	root := tracer.StartSpan("root")
	r := httptest.NewRequest("GET", "/user/123", nil)
	err := tracer.Inject(root.Context(), tracer.HTTPHeadersCarrier(r.Header))
	assert.Nil(err)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	// verify traces look good
	assert.True(called)
	assert.True(traced)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("foobar", span.Tag(ext.ServiceName))
	assert.Equal("echony", span.Tag("test.echo"))
	assert.Contains(span.Tag(ext.ResourceName), "/user/:id")
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal(1.0, span.Tag(ext.EventSampleRate))
	assert.Equal(root.Context().SpanID(), span.ParentID())
	assert.Equal("labstack/echo.v4", span.Tag(ext.Component))
	assert.Equal(string(instrumentation.PackageLabstackEchoV4), span.Integration())
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))

	assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	// setup
	router := echo.New()
	router.Use(Middleware(WithService("foobar")))
	errWant := errors.New("oh no")

	// a handler with an error and make the requests
	router.GET("/err", func(c echo.Context) error {
		_, traced = tracer.SpanFromContext(c.Request().Context())
		called = true

		err := errWant
		c.Error(err)
		return err
	})
	r := httptest.NewRequest("GET", "/err", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	// verify the errors and status are correct
	assert.True(called)
	assert.True(traced)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("foobar", span.Tag(ext.ServiceName))
	assert.Equal("500", span.Tag(ext.HTTPCode))
	require.NotNil(t, span.Tag(ext.ErrorMsg))
	assert.Equal(errWant.Error(), span.Tag(ext.ErrorMsg))
	assert.Equal("labstack/echo.v4", span.Tag(ext.Component))
	assert.Equal(string(instrumentation.PackageLabstackEchoV4), span.Integration())
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

func TestErrorHandling(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	// setup
	router := echo.New()
	router.HTTPErrorHandler = func(_ error, ctx echo.Context) {
		ctx.Response().WriteHeader(http.StatusInternalServerError)
	}
	router.Use(Middleware(WithService("foobar")))
	errWant := errors.New("oh no")

	// a handler with an error and make the requests
	router.GET("/err", func(c echo.Context) error {
		_, traced = tracer.SpanFromContext(c.Request().Context())
		called = true
		return errWant
	})
	r := httptest.NewRequest("GET", "/err", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	// verify the errors and status are correct
	assert.True(called)
	assert.True(traced)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("foobar", span.Tag(ext.ServiceName))
	assert.Equal("500", span.Tag(ext.HTTPCode))
	require.NotNil(t, span.Tag(ext.ErrorMsg))
	assert.Equal(errWant.Error(), span.Tag(ext.ErrorMsg))
	assert.Equal("labstack/echo.v4", span.Tag(ext.Component))
	assert.Equal(string(instrumentation.PackageLabstackEchoV4), span.Integration())
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

func TestStatusError(t *testing.T) {
	for _, tt := range []struct {
		isStatusError             func(statusCode int) bool
		err                       error
		code                      string
		handler                   func(_ echo.Context) error
		envServerErrorStatusesVal string
	}{
		{
			err:  errors.New("oh no"),
			code: "500",
			handler: func(_ echo.Context) error {
				return errors.New("oh no")
			},
		},
		{
			err:  echo.NewHTTPError(http.StatusInternalServerError, "my error message"),
			code: "500",
			handler: func(_ echo.Context) error {
				return echo.NewHTTPError(http.StatusInternalServerError, "my error message")
			},
		},
		{
			err:  nil,
			code: "400",
			handler: func(_ echo.Context) error {
				return echo.NewHTTPError(http.StatusBadRequest, "my error message")
			},
		},
		{
			isStatusError: func(statusCode int) bool { return statusCode >= 400 && statusCode < 500 },
			err:           nil,
			code:          "500",
			handler: func(_ echo.Context) error {
				return errors.New("oh no")
			},
		},
		{
			isStatusError: func(statusCode int) bool { return statusCode >= 400 && statusCode < 500 },
			err:           nil,
			code:          "500",
			handler: func(_ echo.Context) error {
				return echo.NewHTTPError(http.StatusInternalServerError, "my error message")
			},
		},
		{
			isStatusError: func(statusCode int) bool { return statusCode >= 400 },
			err:           echo.NewHTTPError(http.StatusBadRequest, "my error message"),
			code:          "400",
			handler: func(_ echo.Context) error {
				return echo.NewHTTPError(http.StatusBadRequest, "my error message")
			},
		},
		{
			isStatusError: func(statusCode int) bool { return statusCode >= 200 },
			err:           fmt.Errorf("201: Created"),
			code:          "201",
			handler: func(c echo.Context) error {
				c.JSON(201, map[string]string{"status": "ok", "type": "test"})
				return nil
			},
		},
		{
			isStatusError: func(statusCode int) bool { return statusCode >= 200 },
			err:           fmt.Errorf("200: OK"),
			code:          "200",
			handler: func(c echo.Context) error {
				// It's not clear if unset (0) status is possible naturally, but we can simulate that situation.
				c.Response().Status = 0
				return nil
			},
		},
		{
			isStatusError: nil,
			err:           echo.NewHTTPError(http.StatusInternalServerError, "my error message"),
			code:          "500",
			handler: func(_ echo.Context) error {
				return echo.NewHTTPError(http.StatusInternalServerError, "my error message")
			},
			envServerErrorStatusesVal: "500",
		},
		// integration-level config applies regardless of envvar
		{
			isStatusError: func(statusCode int) bool { return statusCode == 400 },
			err:           echo.NewHTTPError(http.StatusBadRequest, "my error message"),
			code:          "400",
			handler: func(_ echo.Context) error {
				return echo.NewHTTPError(http.StatusBadRequest, "my error message")
			},
			envServerErrorStatusesVal: "500",
		},
		// envvar impact is discarded if integration-level config has been applied
		{
			isStatusError: func(statusCode int) bool { return statusCode == 400 },
			err:           nil,
			code:          "500",
			handler: func(_ echo.Context) error {
				return echo.NewHTTPError(http.StatusInternalServerError, "my error message")
			},
		},
	} {
		t.Run("", func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()

			if tt.envServerErrorStatusesVal != "" {
				t.Setenv(envServerErrorStatuses, tt.envServerErrorStatusesVal)
			}

			router := echo.New()
			opts := []Option{WithService("foobar")}
			if tt.isStatusError != nil {
				opts = append(opts, WithStatusCheck(tt.isStatusError))
			}
			router.Use(Middleware(opts...))
			router.GET("/err", tt.handler)
			r := httptest.NewRequest("GET", "/err", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)

			spans := mt.FinishedSpans()
			assert.Len(spans, 1)
			span := spans[0]
			assert.Equal("http.request", span.OperationName())
			assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
			assert.Equal("foobar", span.Tag(ext.ServiceName))
			assert.Contains(span.Tag(ext.ResourceName), "/err")
			assert.Equal(tt.code, span.Tag(ext.HTTPCode))
			assert.Equal("GET", span.Tag(ext.HTTPMethod))
			err := span.Tag(ext.ErrorMsg)
			if tt.err != nil {
				assert.NotNil(err)
				assert.Equal(tt.err.Error(), err)
			} else {
				assert.Nil(err)
			}
		})
	}
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)
	router := echo.New()
	var called, traced bool

	router.GET("/ping", func(c echo.Context) error {
		// Assert we don't have a span on the context.
		called = true
		_, traced = tracer.SpanFromContext(c.Request().Context())
		return c.NoContent(200)
	})

	r := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, r)
	assert.True(called)
	assert.False(traced)
}

func TestNoDebugStack(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	// setup
	router := echo.New()
	router.Use(Middleware(NoDebugStack()))
	errWant := errors.New("oh no")

	// a handler with an error and make the requests
	router.GET("/err", func(c echo.Context) error {
		_, traced = tracer.SpanFromContext(c.Request().Context())
		called = true

		err := errWant
		c.Error(err)
		return err
	})
	r := httptest.NewRequest("GET", "/err", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	// verify the error is correct and the stacktrace is disabled
	assert.True(called)
	assert.True(traced)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	require.NotNil(t, span.Tag(ext.ErrorMsg))
	assert.Equal(errWant.Error(), span.Tag(ext.ErrorMsg))
	assert.Empty(span.Tags()[ext.ErrorStack])
	assert.Equal("labstack/echo.v4", span.Tag(ext.Component))
	assert.Equal(string(instrumentation.PackageLabstackEchoV4), span.Integration())
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

func TestIgnoreRequestFunc(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	// setup
	ignoreRequestFunc := func(_ echo.Context) bool {
		return true
	}
	router := echo.New()
	router.Use(Middleware(WithIgnoreRequest(ignoreRequestFunc)))

	// a handler with an error and make the requests
	router.GET("/err", func(c echo.Context) error {
		_, traced = tracer.SpanFromContext(c.Request().Context())
		called = true
		return nil
	})
	r := httptest.NewRequest("GET", "/err", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	// verify the error is correct and the stacktrace is disabled
	assert.True(called)
	assert.False(traced)

	spans := mt.FinishedSpans()
	assert.Len(spans, 0)
}

type testCustomError struct {
	TestCode int
}

// Error satisfies the apierror interface
func (e *testCustomError) Error() string {
	return "test"
}

func TestWithErrorTranslator(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	// setup
	translateError := func(e error) (*echo.HTTPError, bool) {
		return &echo.HTTPError{
			Message: e.(*testCustomError).Error(),
			Code:    e.(*testCustomError).TestCode,
		}, true
	}
	router := echo.New()
	router.Use(Middleware(WithErrorTranslator(translateError)))

	// a handler with an error and make the requests
	router.GET("/err", func(c echo.Context) error {
		_, traced = tracer.SpanFromContext(c.Request().Context())
		called = true
		return &testCustomError{
			TestCode: 401,
		}
	})
	r := httptest.NewRequest("GET", "/err", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	// verify the error is correct and the stacktrace is disabled
	assert.True(called)
	assert.True(traced)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Contains(span.Tag(ext.ResourceName), "/err")
	assert.Equal("401", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
}

func TestWithErrorCheck(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		opts    []Option
		wantErr error
	}{
		{
			name: "ignore-4xx-404-error",
			err: &echo.HTTPError{
				Code:     http.StatusNotFound,
				Message:  "not found",
				Internal: errors.New("not found"),
			},
			opts: []Option{
				WithErrorCheck(func(err error) bool {
					var he *echo.HTTPError
					if errors.As(err, &he) {
						// do not tag 4xx errors
						return !(he.Code < 500 && he.Code >= 400)
					}
					return true
				}),
			},
			wantErr: nil, // 404 is returned, hence not tagged
		},
		{
			name: "ignore-4xx-500-error",
			err: &echo.HTTPError{
				Code:     http.StatusInternalServerError,
				Message:  "internal error",
				Internal: errors.New("internal error"),
			},
			opts: []Option{
				WithErrorCheck(func(err error) bool {
					var he *echo.HTTPError
					if errors.As(err, &he) {
						// do not tag 4xx errors
						return !(he.Code < 500 && he.Code >= 400)
					}
					return true
				}),
			},
			wantErr: &echo.HTTPError{
				Code:     http.StatusInternalServerError,
				Message:  "internal error",
				Internal: errors.New("internal error"),
			}, // this is 500, tagged
		},
		{
			name: "ignore-none",
			err:  errors.New("any error"),
			opts: []Option{
				WithErrorCheck(func(_ error) bool {
					return true
				}),
			},
			wantErr: errors.New("any error"),
		},
		{
			name: "ignore-all",
			err:  errors.New("any error"),
			opts: []Option{
				WithErrorCheck(func(_ error) bool {
					return false
				}),
			},
			wantErr: nil,
		},
		{
			// withErrorCheck also runs for the errors created from the WithStatusCheck option.
			name: "ignore-errors-from-status-check",
			err: &echo.HTTPError{
				Code:     http.StatusNotFound,
				Message:  "internal error",
				Internal: errors.New("internal error"),
			},
			opts: []Option{
				WithStatusCheck(func(statusCode int) bool {
					return statusCode == http.StatusNotFound
				}),
				WithErrorCheck(func(_ error) bool {
					return false
				}),
			},
			wantErr: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			router := echo.New()
			router.Use(Middleware(tt.opts...))
			var called, traced bool

			// always return the specified error
			router.GET("/err", func(c echo.Context) error {
				_, traced = tracer.SpanFromContext(c.Request().Context())
				called = true
				return tt.err
			})
			r := httptest.NewRequest(http.MethodGet, "/err", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)

			assert.True(t, called)
			assert.True(t, traced)
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1) // fail at once if there is no span

			span := spans[0]
			if tt.wantErr == nil {
				assert.NotContains(t, span.Tags(), ext.ErrorMsg)
				return
			}
			assert.Equal(t, tt.wantErr.Error(), span.Tag(ext.ErrorMsg))
		})
	}
}

// TestPropagationBehaviorExtract is an integration test verifying DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT
// through a real HTTP middleware stack.
// Echotrace was used because it was
// already available; any HTTP middleware integration would work equivalently.
func TestPropagationBehaviorExtract(t *testing.T) {
	tests := []struct {
		name            string
		behavior        string
		wantSameTraceID bool // server span continues the root trace
		wantParentID    bool // server span has root as parent
		wantSpanLinks   bool // server span has a span link to the root context
		wantBaggage     bool // baggage from root is propagated to server span
	}{
		{
			name:            "continue",
			behavior:        "continue",
			wantSameTraceID: true,
			wantParentID:    true,
			wantSpanLinks:   false,
			wantBaggage:     true,
		},
		{
			name:            "restart",
			behavior:        "restart",
			wantSameTraceID: false,
			wantParentID:    false,
			wantSpanLinks:   true,
			wantBaggage:     true,
		},
		{
			name:            "ignore",
			behavior:        "ignore",
			wantSameTraceID: false,
			wantParentID:    false,
			wantSpanLinks:   false,
			wantBaggage:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT", tc.behavior)

			mt := mocktracer.Start()
			defer mt.Stop()

			router := echo.New()
			router.Use(Middleware(WithService("test-service")))
			router.GET("/test", func(c echo.Context) error {
				return c.NoContent(200)
			})

			root := tracer.StartSpan("incoming-request")
			root.SetBaggageItem("test-baggage", "baggage-value")

			r := httptest.NewRequest("GET", "/test", nil)
			err := tracer.Inject(root.Context(), tracer.HTTPHeadersCarrier(r.Header))
			require.NoError(t, err)

			router.ServeHTTP(httptest.NewRecorder(), r)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			span := spans[0]

			if tc.wantSameTraceID {
				assert.Equal(t, root.Context().TraceID(), span.Context().TraceID())
			} else {
				assert.NotEqual(t, root.Context().TraceID(), span.Context().TraceID())
			}

			if tc.wantParentID {
				assert.Equal(t, root.Context().SpanID(), span.ParentID())
			} else {
				assert.Equal(t, uint64(0), span.ParentID())
			}

			links := span.Links()
			if tc.wantSpanLinks {
				require.Len(t, links, 1)
				assert.Equal(t, root.Context().SpanID(), links[0].SpanID)
				assert.Equal(t, map[string]string{"reason": "propagation_behavior_extract", "context_headers": "datadog"}, links[0].Attributes)
			} else {
				assert.Empty(t, links)
			}

			var baggageItems []string
			span.Context().ForeachBaggageItem(func(k, v string) bool {
				baggageItems = append(baggageItems, k+"="+v)
				return true
			})
			if tc.wantBaggage {
				assert.Contains(t, baggageItems, "test-baggage=baggage-value")
			} else {
				assert.Empty(t, baggageItems)
			}
		})
	}
}

func TestDatadogSemantics(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
	}{
		{name: "unset"},
		{name: "disabled", value: "false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setEchoHTTPConfig(t, tt.value)
			t.Setenv("DD_TRACE_RESOURCE_RENAMING_ENABLED", "true")
			httptrace.ResetCfg()

			matched := traceEchoRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK)
			assert.Equal(t, "GET /users/:id", matched.Tag(ext.ResourceName))
			assert.Equal(t, "/users/:id", matched.Tag(ext.HTTPRoute))
			assert.Equal(t, "GET", matched.Tag(ext.HTTPMethod))
			assert.Equal(t, "http://example.com/users/123", matched.Tag(ext.HTTPURL))
			assert.Equal(t, "200", matched.Tag(ext.HTTPCode))
			assert.Nil(t, matched.Tag(ext.HTTPEndpoint))
			assert.Nil(t, matched.Tag(ext.HTTPRequestMethod))
			assert.Nil(t, matched.Tag(ext.URLPath))
			assert.Nil(t, matched.Tag(ext.HTTPResponseStatusCode))

			unmatched := traceEchoRequestWithRoute(t, http.MethodGet, "http://example.com/missing", http.StatusOK, "", "", nil)
			assert.Equal(t, "GET ", unmatched.Tag(ext.ResourceName))
			assert.Contains(t, unmatched.Tags(), ext.HTTPRoute)
			assert.Equal(t, "", unmatched.Tag(ext.HTTPRoute))
		})
	}
}

func TestOTelSemantics(t *testing.T) {
	setEchoHTTPConfig(t, "true")
	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	httptrace.ResetCfg()

	t.Run("route and attributes", func(t *testing.T) {
		span := traceEchoRequest(t, http.MethodGet, "http://example.com/users/123?password=secret&keep=value", http.StatusOK, WithCustomTag("echo.custom", "value"))
		assert.Equal(t, "GET /users/:id", span.Tag(ext.ResourceName))
		assert.Equal(t, "/users/:id", span.Tag(ext.HTTPRoute))
		assert.Equal(t, "GET", span.Tag(ext.HTTPRequestMethod))
		assert.Nil(t, span.Tag(ext.HTTPRequestMethodOriginal))
		assert.Equal(t, "/users/123", span.Tag(ext.URLPath))
		assert.Equal(t, "http", span.Tag(ext.URLScheme))
		assert.Equal(t, "<redacted>&keep=value", span.Tag(ext.URLQuery))
		assert.Equal(t, "example.com", span.Tag(ext.ServerAddress))
		assert.Equal(t, "semantic-agent", span.Tag(ext.UserAgentOriginal))
		assert.Equal(t, "203.0.113.10", span.Tag(ext.ClientAddress))
		assert.Equal(t, "192.0.2.1", span.Tag(ext.NetworkPeerAddress))
		assert.Equal(t, "200", span.Tag(ext.HTTPResponseStatusCode))
		assert.Equal(t, "semantic-service", span.Tag(ext.ServiceName))
		assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind))
		assert.Equal(t, "labstack/echo.v4", span.Tag(ext.Component))
		assert.Equal(t, string(instrumentation.PackageLabstackEchoV4), span.Integration())
		assert.Equal(t, "http.request", span.OperationName())
		assert.Equal(t, ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal(t, "value", span.Tag("echo.custom"))
		assert.Nil(t, span.Tag(ext.HTTPMethod))
		assert.Nil(t, span.Tag(ext.HTTPURL))
		assert.Nil(t, span.Tag(ext.HTTPCode))
		assert.Nil(t, span.Tag(ext.HTTPUserAgent))
		assert.Nil(t, span.Tag(ext.HTTPClientIP))
		assert.Nil(t, span.Tag(ext.NetworkClientIP))
	})

	t.Run("route is invariant across parameters", func(t *testing.T) {
		first := traceEchoRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK)
		second := traceEchoRequest(t, http.MethodGet, "http://example.com/users/456", http.StatusOK)
		assert.Equal(t, "GET /users/:id", first.Tag(ext.ResourceName))
		assert.Equal(t, first.Tag(ext.ResourceName), second.Tag(ext.ResourceName))
	})

	for _, tt := range []struct {
		name         string
		method       string
		target       string
		routeMethod  string
		route        string
		wantResource string
		wantRoute    any
		wantMethod   string
		wantOriginal any
		wantPath     string
		wantStatus   string
	}{
		{name: "not found", method: "gEt", target: "http://example.com/actual/path", wantResource: "GET", wantMethod: "GET", wantOriginal: "gEt", wantPath: "/actual/path", wantStatus: "404"},
		{name: "method not allowed", method: http.MethodPost, target: "http://example.com/users/123", routeMethod: http.MethodGet, route: "/users/:id", wantResource: "POST /users/:id", wantRoute: "/users/:id", wantMethod: "POST", wantPath: "/users/123", wantStatus: "405"},
		{name: "unknown method with route", method: "PROPFIND", target: "http://example.com/users/123", routeMethod: "PROPFIND", route: "/users/:id", wantResource: "HTTP /users/:id", wantRoute: "/users/:id", wantMethod: "_OTHER", wantOriginal: "PROPFIND", wantPath: "/users/123", wantStatus: "200"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			span := traceEchoRequestWithRoute(t, tt.method, tt.target, http.StatusOK, tt.routeMethod, tt.route, nil)
			assert.Equal(t, tt.wantResource, span.Tag(ext.ResourceName))
			assert.Equal(t, tt.wantRoute, span.Tag(ext.HTTPRoute))
			if tt.wantRoute == nil {
				assert.NotContains(t, span.Tags(), ext.HTTPRoute)
			}
			assert.Equal(t, tt.wantMethod, span.Tag(ext.HTTPRequestMethod))
			assert.Equal(t, tt.wantOriginal, span.Tag(ext.HTTPRequestMethodOriginal))
			assert.Equal(t, tt.wantPath, span.Tag(ext.URLPath))
			assert.Equal(t, tt.wantStatus, span.Tag(ext.HTTPResponseStatusCode))
		})
	}
}

func TestOTelSemanticsStatus(t *testing.T) {
	setEchoHTTPConfig(t, "true")

	for _, tt := range []struct {
		name          string
		status        int
		isStatusError func(int) bool
		errCheck      func(error) bool
		wantErrorType any
	}{
		{name: "success", status: http.StatusOK},
		{name: "client error", status: http.StatusBadRequest},
		{name: "server error", status: http.StatusInternalServerError, wantErrorType: "500"},
		{name: "custom client error inclusion", status: http.StatusBadRequest, isStatusError: func(status int) bool { return status == http.StatusBadRequest }, wantErrorType: "400"},
		{name: "custom success inclusion", status: http.StatusCreated, isStatusError: func(status int) bool { return status == http.StatusCreated }, wantErrorType: "201"},
		{name: "custom exclusion", status: http.StatusInternalServerError, isStatusError: func(int) bool { return false }},
		{name: "error check exclusion", status: http.StatusInternalServerError, errCheck: func(error) bool { return false }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.isStatusError != nil {
				opts = append(opts, WithStatusCheck(tt.isStatusError))
			}
			if tt.errCheck != nil {
				opts = append(opts, WithErrorCheck(tt.errCheck))
			}
			span := traceEchoRequest(t, http.MethodGet, "http://example.com/users/123", tt.status, opts...)
			assert.Equal(t, tt.wantErrorType, span.Tag(ext.ErrorType))
		})
	}
}

func TestOTelSemanticsErrors(t *testing.T) {
	setEchoHTTPConfig(t, "true")
	responseErr := errors.New("oh no")

	t.Run("retained real error", func(t *testing.T) {
		span := traceEchoError(t, responseErr)
		require.NotNil(t, span.Tag(ext.ErrorType))
		assert.NotEqual(t, "500", span.Tag(ext.ErrorType))
		assert.Equal(t, responseErr.Error(), span.Tag(ext.ErrorMsg))
		assert.Equal(t, "500", span.Tag(ext.HTTPResponseStatusCode))
	})

	t.Run("ignored real error", func(t *testing.T) {
		span := traceEchoError(t, responseErr, WithErrorCheck(func(error) bool { return false }))
		assert.Nil(t, span.Tag(ext.ErrorType))
		assert.Nil(t, span.Tag(ext.ErrorMsg))
	})

	t.Run("translator and status inclusion", func(t *testing.T) {
		err := &testCustomError{TestCode: http.StatusBadRequest}
		span := traceEchoError(t, err,
			WithErrorTranslator(func(err error) (*echo.HTTPError, bool) {
				return echo.NewHTTPError(err.(*testCustomError).TestCode), true
			}),
			WithStatusCheck(func(status int) bool { return status == http.StatusBadRequest }),
		)
		require.NotNil(t, span.Tag(ext.ErrorType))
		assert.NotEqual(t, "400", span.Tag(ext.ErrorType))
		assert.Equal(t, "400", span.Tag(ext.HTTPResponseStatusCode))
	})

	t.Run("no debug stack", func(t *testing.T) {
		span := traceEchoError(t, responseErr, NoDebugStack())
		assert.Empty(t, span.Tag(ext.ErrorStack))
		assert.Equal(t, responseErr.Error(), span.Tag(ext.ErrorMsg))
	})
}

func TestOTelSemanticsContextPropagationAndWrap(t *testing.T) {
	setEchoHTTPConfig(t, "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	var handlerSpan *tracer.Span
	router := Wrap(echo.New(), WithService("semantic-service"))
	router.GET("/users/:id", func(c echo.Context) error {
		handlerSpan, _ = tracer.SpanFromContext(c.Request().Context())
		return c.NoContent(http.StatusOK)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/123", nil))

	require.NotNil(t, handlerSpan)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/:id", spans[0].Tag(ext.ResourceName))
}

func TestOTelSemanticsAppSecRouteParams(t *testing.T) {
	setEchoHTTPConfig(t, "true")
	testutils.StartAppSec(t)
	httptrace.ResetCfg()

	mt := mocktracer.Start()
	defer mt.Stop()
	router := Wrap(echo.New(), WithService("semantic-service"))
	router.GET("/users/:id", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/users/appscan_fingerprint", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	router.ServeHTTP(httptest.NewRecorder(), req)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/:id", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, "/users/:id", spans[0].Tag(ext.HTTPRoute))
	assert.Equal(t, "/users/:id", spans[0].Tag(ext.HTTPEndpoint))
	assert.Equal(t, "203.0.113.10", spans[0].Tag(ext.ClientAddress))
	assert.Equal(t, "192.0.2.1", spans[0].Tag(ext.NetworkPeerAddress))
	assert.Nil(t, spans[0].Tag(ext.HTTPClientIP))
	assert.Nil(t, spans[0].Tag(ext.NetworkClientIP))
	event, ok := spans[0].Tag("_dd.appsec.json").(string)
	require.True(t, ok)
	assert.Contains(t, event, "server.request.path_params")
	assert.Contains(t, event, "appscan_fingerprint")
}

func traceEchoRequest(t *testing.T, method, target string, status int, opts ...Option) *mocktracer.Span {
	t.Helper()
	return traceEchoRequestWithRoute(t, method, target, status, method, "/users/:id", nil, opts...)
}

func traceEchoRequestWithRoute(t *testing.T, method, target string, status int, routeMethod, route string, handler echo.HandlerFunc, opts ...Option) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	router := echo.New()
	router.Use(Middleware(append([]Option{WithService("semantic-service")}, opts...)...))
	if route != "" {
		if handler == nil {
			handler = func(c echo.Context) error { return c.NoContent(status) }
		}
		router.Add(routeMethod, route, handler)
	}

	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("User-Agent", "semantic-agent")
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	router.ServeHTTP(httptest.NewRecorder(), req)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}

func traceEchoError(t *testing.T, responseErr error, opts ...Option) *mocktracer.Span {
	t.Helper()
	return traceEchoRequestWithRoute(t, http.MethodGet, "http://example.com/error", http.StatusOK, http.MethodGet, "/error", func(echo.Context) error {
		return responseErr
	}, opts...)
}

func setEchoHTTPConfig(t *testing.T, otel string) {
	t.Helper()
	oldOTel, hadOTel := os.LookupEnv("DD_TRACE_OTEL_SEMANTICS_ENABLED")
	if otel == "" {
		require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
	} else {
		require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", otel))
	}
	require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
	httptrace.ResetCfg()
	t.Cleanup(func() {
		if hadOTel {
			require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", oldOTel))
		} else {
			require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
		}
		require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
		httptrace.ResetCfg()
		tracer.Stop()
	})
}

func BenchmarkEchoWithTracing(b *testing.B) {
	tracer.Start(tracer.WithLogger(testutils.DiscardLogger()))
	defer tracer.Stop()

	mux := echo.New()
	mux.Use(Middleware())
	mux.GET("/200", func(c echo.Context) error {
		return c.NoContent(200)
	})
	r := httptest.NewRequest("GET", "/200", nil)
	w := httptest.NewRecorder()

	b.ResetTimer()
	for b.Loop() {
		mux.ServeHTTP(w, r)
	}
}
