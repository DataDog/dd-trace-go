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
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"

	"github.com/labstack/echo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	router := echo.New()
	router.Use(Middleware(WithServiceName("foobar")))
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
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return r
	}
	t.Run("default-off", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		htArgs := []string{"h!e@a-d.e*r", "2header", "3header"}
		setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]
		for _, arg := range htArgs {
			_, tag := normalizer.HeaderTag(arg)
			assert.NotContains(s.Tags(), tag)
		}
	})
	t.Run("integration", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		htArgs := []string{"h!e@a-d.e*r", "2header:tag"}
		r := setupReq(WithHeaderTags(htArgs))
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		for _, arg := range htArgs {
			header, tag := normalizer.HeaderTag(arg)
			assert.Equal(strings.Join(r.Header.Values(header), ","), s.Tags()[tag])
		}
	})

	t.Run("global", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		header, tag := normalizer.HeaderTag("3header")
		globalconfig.SetHeaderTag(header, tag)

		r := setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		assert.Equal(strings.Join(r.Header.Values(header), ","), s.Tags()[tag])
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		globalH, globalT := normalizer.HeaderTag("3header")
		globalconfig.SetHeaderTag(globalH, globalT)

		htArgs := []string{"h!e@a-d.e*r", "2header:tag"}
		r := setupReq(WithHeaderTags(htArgs))
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		for _, arg := range htArgs {
			header, tag := normalizer.HeaderTag(arg)
			assert.Equal(strings.Join(r.Header.Values(header), ","), s.Tags()[tag])
		}
		assert.NotContains(s.Tags(), globalT)
	})
}

func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	router := echo.New()
	router.Use(Middleware(WithServiceName("foobar"), WithAnalytics(false)))
	router.GET("/user/:id", func(c echo.Context) error {
		called = true
		var span tracer.Span
		span, traced = tracer.SpanFromContext(c.Request().Context())

		// we patch the span on the request context.
		span.SetTag("test.echo", "echony")
		assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")
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
	assert.Equal("labstack/echo", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))

	assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
}

func TestTraceAnalytics(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	router := echo.New()
	router.Use(Middleware(WithServiceName("foobar"), WithAnalytics(true)))
	router.GET("/user/:id", func(c echo.Context) error {
		called = true
		var span tracer.Span
		span, traced = tracer.SpanFromContext(c.Request().Context())

		// we patch the span on the request context.
		span.SetTag("test.echo", "echony")
		assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")
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
	assert.Equal("labstack/echo", span.Tag(ext.Component))
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
	router.Use(Middleware(WithServiceName("foobar")))
	wantErr := errors.New("oh no")

	// a handler with an error and make the requests
	router.GET("/err", func(c echo.Context) error {
		_, traced = tracer.SpanFromContext(c.Request().Context())
		called = true

		err := wantErr
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
	require.NotNil(t, span.Tag(ext.Error))
	assert.Equal(wantErr.Error(), span.Tag(ext.Error).(error).Error())
	assert.Equal("labstack/echo", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

func TestErrorHandling(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var called, traced bool

	// setup
	router := echo.New()
	router.HTTPErrorHandler = func(err error, ctx echo.Context) {
		ctx.Response().WriteHeader(http.StatusInternalServerError)
	}
	router.Use(Middleware(WithServiceName("foobar")))
	wantErr := errors.New("oh no")

	// a handler with an error and make the requests
	router.GET("/err", func(c echo.Context) error {
		_, traced = tracer.SpanFromContext(c.Request().Context())
		called = true
		return wantErr
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
	require.NotNil(t, span.Tag(ext.Error))
	assert.Equal(wantErr.Error(), span.Tag(ext.Error).(error).Error())
	assert.Equal("labstack/echo", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

func TestStatusError(t *testing.T) {
	for _, tt := range []struct {
		isStatusError func(statusCode int) bool
		err           error
		code          string
		handler       func(c echo.Context) error
	}{
		{
			err:  errors.New("oh no"),
			code: "500",
			handler: func(c echo.Context) error {
				return errors.New("oh no")
			},
		},
		{
			err:  echo.NewHTTPError(http.StatusInternalServerError, "my error message"),
			code: "500",
			handler: func(c echo.Context) error {
				return echo.NewHTTPError(http.StatusInternalServerError, "my error message")
			},
		},
		{
			err:  nil,
			code: "400",
			handler: func(c echo.Context) error {
				return echo.NewHTTPError(http.StatusBadRequest, "my error message")
			},
		},
		{
			isStatusError: func(statusCode int) bool { return statusCode >= 400 && statusCode < 500 },
			err:           nil,
			code:          "500",
			handler: func(c echo.Context) error {
				return errors.New("oh no")
			},
		},
		{
			isStatusError: func(statusCode int) bool { return statusCode >= 400 && statusCode < 500 },
			err:           nil,
			code:          "500",
			handler: func(c echo.Context) error {
				return echo.NewHTTPError(http.StatusInternalServerError, "my error message")
			},
		},
		{
			isStatusError: func(statusCode int) bool { return statusCode >= 400 },
			err:           echo.NewHTTPError(http.StatusBadRequest, "my error message"),
			code:          "400",
			handler: func(c echo.Context) error {
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
	} {
		t.Run("", func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()

			router := echo.New()
			opts := []Option{WithServiceName("foobar")}
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
			err := span.Tag(ext.Error)
			if tt.err != nil {
				assert.NotNil(err)
				require.NotNil(t, span.Tag(ext.Error))
				assert.Equal(tt.err.Error(), err.(error).Error())
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
	wantErr := errors.New("oh no")

	// a handler with an error and make the requests
	router.GET("/err", func(c echo.Context) error {
		_, traced = tracer.SpanFromContext(c.Request().Context())
		called = true

		err := wantErr
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
	require.NotNil(t, span.Tag(ext.Error))
	assert.Equal(wantErr.Error(), span.Tag(ext.Error).(error).Error())
	assert.Equal("<debug stack disabled>", span.Tag(ext.ErrorStack))
	assert.Equal("labstack/echo", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := echo.New()
		mux.Use(Middleware(opts...))
		mux.GET("/200", func(c echo.Context) error {
			return c.NoContent(200)
		})
		r := httptest.NewRequest("GET", "/200", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)

		return mt.FinishedSpans()
	})
	namingschematest.NewHTTPServerTest(genSpans, "echo")(t)
}

func BenchmarkEchoNoTracing(b *testing.B) {
	mux := echo.New()
	mux.GET("/200", func(c echo.Context) error {
		return c.NoContent(200)
	})
	r := httptest.NewRequest("GET", "/200", nil)
	w := httptest.NewRecorder()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.ServeHTTP(w, r)
	}
}

func BenchmarkEchoWithTracing(b *testing.B) {
	tracer.Start(tracer.WithLogger(log.DiscardLogger{}))
	defer tracer.Stop()

	mux := echo.New()
	mux.Use(Middleware())
	mux.GET("/200", func(c echo.Context) error {
		return c.NoContent(200)
	})
	r := httptest.NewRequest("GET", "/200", nil)
	w := httptest.NewRecorder()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.ServeHTTP(w, r)
	}
}
