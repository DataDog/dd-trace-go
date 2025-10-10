// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gin

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
)

func init() {
	gin.SetMode(gin.ReleaseMode) // silence annoying log msgs
}

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := gin.New()
	router.Use(Middleware("foobar"))
	router.GET("/user/:id", func(c *gin.Context) {
		_, ok := tracer.SpanFromContext(c.Request.Context())
		assert.True(ok)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, r)
}

func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := gin.New()
	router.Use(Middleware("foobar"))
	router.GET("/user/:id", func(c *gin.Context) {
		span, ok := tracer.SpanFromContext(c.Request.Context())
		assert.True(ok)
		assert.Equal(mocktracer.MockSpan(span).Tag(ext.ServiceName), "foobar")
		id := c.Param("id")
		c.Writer.Write([]byte(id))
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	// do and verify the request
	router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 200)

	// verify traces look good
	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("foobar", span.Tag(ext.ServiceName))
	assert.Contains(span.Tag(ext.ResourceName), "GET /user/:id")
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.Equal("gin-gonic/gin", span.Tag(ext.Component))
	assert.Equal(componentName, span.Integration())
}

func TestTraceDefaultResponse(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := gin.New()
	router.Use(Middleware("foobar"))
	router.GET("/user/:id", func(c *gin.Context) {
		_, ok := tracer.SpanFromContext(c.Request.Context())
		assert.True(ok)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	// do and verify the request
	router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 200)

	// verify traces look good
	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("foobar", span.Tag(ext.ServiceName))
	assert.Contains(span.Tag(ext.ResourceName), "GET /user/:id")
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.Equal("gin-gonic/gin", span.Tag(ext.Component))
	assert.Equal(componentName, span.Integration())
}

func TestTraceMultipleResponses(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := gin.New()
	router.Use(Middleware("foobar"))
	router.GET("/user/:id", func(c *gin.Context) {
		_, ok := tracer.SpanFromContext(c.Request.Context())
		assert.True(ok)
		c.Status(142)
		c.Writer.WriteString("test")
		c.Status(133)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	// do and verify the request
	router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 142)

	// verify traces look good
	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("foobar", span.Tag(ext.ServiceName))
	assert.Contains(span.Tag(ext.ResourceName), "GET /user/:id")
	assert.Equal("142", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.Equal("gin-gonic/gin", span.Tag(ext.Component))
	assert.Equal(componentName, span.Integration())
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	responseErr := errors.New("oh no")

	t.Run("server error - with error propagation", func(*testing.T) {
		defer mt.Reset()

		router := gin.New()
		router.Use(Middleware("foobar", WithErrorPropagation()))

		// configure a handler that returns an error and 5xx status code
		router.GET("/server_err", func(c *gin.Context) {
			c.AbortWithError(500, responseErr)
		})
		r := httptest.NewRequest("GET", "/server_err", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, 500)

		// verify the errors and status are correct
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		if len(spans) < 1 {
			t.Fatalf("no spans")
		}
		span := spans[0]
		assert.Equal("http.request", span.OperationName())
		assert.Equal("foobar", span.Tag(ext.ServiceName))
		assert.Equal("500", span.Tag(ext.HTTPCode))
		assert.Equal(fmt.Sprintf("Error #01: %s\n", responseErr), span.Tag("gin.errors"))
		// server errors set the ext.ErrorMsg tag
		assert.Equal("oh no", span.Tag(ext.ErrorMsg))
		assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
		assert.Equal("gin-gonic/gin", span.Tag(ext.Component))
		assert.Equal(componentName, span.Integration())
	})

	t.Run("server error - with error propagation - nil Errors in gin context", func(*testing.T) {
		defer mt.Reset()

		router := gin.New()
		router.Use(Middleware("foobar", WithErrorPropagation()))

		// configure a handler that returns an error and 5xx status code
		router.GET("/server_err", func(c *gin.Context) {
			c.AbortWithStatus(500)
		})
		r := httptest.NewRequest("GET", "/server_err", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, 500)

		// verify the errors and status are correct
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		if len(spans) < 1 {
			t.Fatalf("no spans")
		}
		span := spans[0]
		assert.Equal("http.request", span.OperationName())
		assert.Equal("foobar", span.Tag(ext.ServiceName))
		assert.Equal("500", span.Tag(ext.HTTPCode))
		assert.Empty(span.Tag("gin.errors"))
		// server errors set the ext.ErrorMsg tag
		assert.Equal("500: Internal Server Error", span.Tag(ext.ErrorMsg))
		assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
		assert.Equal("gin-gonic/gin", span.Tag(ext.Component))
		assert.Equal(componentName, span.Integration())
	})

	t.Run("server error - without error propagation", func(*testing.T) {
		defer mt.Reset()

		router := gin.New()
		router.Use(Middleware("foobar"))

		// configure a handler that returns an error and 5xx status code
		router.GET("/server_err", func(c *gin.Context) {
			c.AbortWithError(500, responseErr)
		})
		r := httptest.NewRequest("GET", "/server_err", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, 500)

		// verify the errors and status are correct
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		if len(spans) < 1 {
			t.Fatalf("no spans")
		}
		span := spans[0]
		assert.Equal("http.request", span.OperationName())
		assert.Equal("foobar", span.Tag(ext.ServiceName))
		assert.Equal("500", span.Tag(ext.HTTPCode))
		assert.Equal(fmt.Sprintf("Error #01: %s\n", responseErr), span.Tag("gin.errors"))
		// server errors set the ext.ErrorMsg tag
		assert.Equal("500: Internal Server Error", span.Tag(ext.ErrorMsg))
		assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
		assert.Equal("gin-gonic/gin", span.Tag(ext.Component))
		assert.Equal(componentName, span.Integration())
	})

	t.Run("client error", func(*testing.T) {
		defer mt.Reset()

		router := gin.New()
		router.Use(Middleware("foobar"))

		// configure a handler that returns an error and 4xx status code
		router.GET("/client_err", func(c *gin.Context) {
			c.AbortWithError(418, responseErr)
		})
		r := httptest.NewRequest("GET", "/client_err", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, 418)

		// verify the errors and status are correct
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		if len(spans) < 1 {
			t.Fatalf("no spans")
		}
		span := spans[0]
		assert.Equal("http.request", span.OperationName())
		assert.Equal("foobar", span.Tag(ext.ServiceName))
		assert.Equal("418", span.Tag(ext.HTTPCode))
		assert.Equal(fmt.Sprintf("Error #01: %s\n", responseErr), span.Tag("gin.errors"))
		// client errors do not set the ext.ErrorMsg tag
		assert.Zero(span.Tag(ext.ErrorMsg))
		assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
		assert.Equal("gin-gonic/gin", span.Tag(ext.Component))
		assert.Equal(componentName, span.Integration())
	})
}

func TestHTML(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// setup
	router := gin.New()
	router.Use(Middleware("foobar"))

	// add a template
	tmpl := template.Must(template.New("hello").Parse("hello {{.}}"))
	router.SetHTMLTemplate(tmpl)

	// a handler with an error and make the requests
	router.GET("/hello", func(c *gin.Context) {
		HTML(c, 200, "hello", "world")
	})
	r := httptest.NewRequest("GET", "/hello", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 200)
	assert.Equal("hello world", w.Body.String())

	// verify the errors and status are correct
	spans := mt.FinishedSpans()
	assert.Len(spans, 2)
	for _, s := range spans {
		assert.Equal("foobar", s.Tag(ext.ServiceName), s.String())
		assert.Equal("gin-gonic/gin", s.Tag(ext.Component))
		assert.Equal(componentName, s.Integration())
	}

	var tspan *mocktracer.Span
	for _, s := range spans {
		// we need to pick up the span we're searching for, as the
		// order is not garanteed within the buffer
		if s.OperationName() == "gin.render.html" {
			tspan = s
		}
	}
	assert.NotNil(tspan)
	assert.Equal("hello", tspan.Tag("go.template"))

	_, ok := tspan.Tags()[ext.SpanKind]
	assert.Equal(false, ok)
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)
	router := gin.New()
	router.GET("/ping", func(c *gin.Context) {
		// Assert we don't have a span on the context.
		_, ok := tracer.SpanFromContext(c.Request.Context())
		assert.False(ok)
		c.Writer.Write([]byte("ok"))
	})
	r := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 200)
}

func TestPropagation(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	pspan := tracer.StartSpan("test")
	err := tracer.Inject(pspan.Context(), tracer.HTTPHeadersCarrier(r.Header))
	require.NoError(t, err)

	router := gin.New()
	router.Use(Middleware("foobar"))
	router.GET("/user/:id", func(c *gin.Context) {
		span, ok := tracer.SpanFromContext(c.Request.Context())
		assert.True(ok)
		assert.Equal(mocktracer.MockSpan(span).ParentID(), mocktracer.MockSpan(pspan).SpanID())
	})

	router.ServeHTTP(w, r)
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		router := gin.New()
		router.Use(Middleware("foobar", opts...))
		router.GET("/user/:id", func(_ *gin.Context) {})

		r := httptest.NewRequest("GET", "/user/123", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, r)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalAnalyticsRate(t, 0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalAnalyticsRate(t, 0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}

func TestResourceNamerSettings(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	staticName := "foo"
	staticNamer := func(_ *gin.Context) string {
		return staticName
	}

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		defer mt.Reset()

		router := gin.New()
		router.Use(Middleware("foobar"))

		router.GET("/test", func(c *gin.Context) {
			span, ok := tracer.SpanFromContext(c.Request.Context())
			assert.True(ok)
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ResourceName), "GET /test")
		})

		r := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, r)
	})

	t.Run("custom", func(t *testing.T) {
		assert := assert.New(t)
		defer mt.Reset()

		router := gin.New()
		router.Use(Middleware("foobar", WithResourceNamer(staticNamer)))

		router.GET("/test", func(c *gin.Context) {
			span, ok := tracer.SpanFromContext(c.Request.Context())
			assert.True(ok)
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ResourceName), staticName)
		})

		r := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, r)
	})
}

func TestWithHeaderTags(t *testing.T) {
	setupReq := func(opts ...Option) *http.Request {
		router := gin.New()
		router.Use(Middleware("gin", opts...))

		router.GET("/test", func(c *gin.Context) {
			c.Writer.Write([]byte("test"))
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

func TestIgnoreRequestSettings(t *testing.T) {
	router := gin.New()
	router.Use(Middleware("foobar", WithIgnoreRequest(func(c *gin.Context) bool {
		return strings.HasPrefix(c.Request.URL.Path, "/skip")
	})))

	router.GET("/OK", func(c *gin.Context) {
		c.Writer.Write([]byte("OK"))
	})

	router.GET("/skip", func(c *gin.Context) {
		c.Writer.Write([]byte("Skip"))
	})

	for path, shouldSkip := range map[string]bool{
		"/OK":      false,
		"/skip":    true,
		"/skipfoo": true,
	} {
		mt := mocktracer.Start()
		r := httptest.NewRequest("GET", "http://localhost"+path, nil)
		router.ServeHTTP(httptest.NewRecorder(), r)
		assert.Equal(t, shouldSkip, len(mt.FinishedSpans()) == 0)
		mt.Stop()
	}
}

func TestServiceName(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := gin.New()
		router.Use(Middleware(""))
		router.GET("/ping", func(c *gin.Context) {
			span, ok := tracer.SpanFromContext(c.Request.Context())
			assert.True(ok)
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ServiceName), "gin.router")
			c.Status(200)
		})

		r := httptest.NewRequest("GET", "/ping", nil)
		w := httptest.NewRecorder()

		// do and verify the request
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, 200)

		// verify traces look good
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal("gin.router", span.Tag(ext.ServiceName))
	})

	t.Run("global", func(t *testing.T) {
		testutils.SetGlobalServiceName(t, "global-service")

		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := gin.New()
		router.Use(Middleware(""))
		router.GET("/ping", func(c *gin.Context) {
			span, ok := tracer.SpanFromContext(c.Request.Context())
			assert.True(ok)
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ServiceName), "global-service")
			c.Status(200)
		})

		r := httptest.NewRequest("GET", "/ping", nil)
		w := httptest.NewRecorder()

		// do and verify the request
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, 200)

		// verify traces look good
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal("global-service", span.Tag(ext.ServiceName))
	})

	t.Run("custom", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := gin.New()
		router.Use(Middleware("my-service"))
		router.GET("/ping", func(c *gin.Context) {
			span, ok := tracer.SpanFromContext(c.Request.Context())
			assert.True(ok)
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ServiceName), "my-service")
			c.Status(200)
		})

		r := httptest.NewRequest("GET", "/ping", nil)
		w := httptest.NewRecorder()

		// do and verify the request
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, 200)

		// verify traces look good
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal("my-service", span.Tag(ext.ServiceName))
	})
}

// TestTracerStartedMultipleTimes tests a v2 regression where the global service name was being set to an empty string
// when the tracer is started more than once.
func TestTracerStartedMultipleTimes(t *testing.T) {
	tt1 := testtracer.Start(t)
	defer tt1.Stop()
	tt2 := testtracer.Start(t, testtracer.WithTracerStartOpts(tracer.WithService("global_service")))
	defer tt2.Stop()

	router := gin.New()
	router.Use(Middleware(""))
	router.GET("/ping", func(c *gin.Context) {
		c.Status(200)
	})

	r := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(t, response.StatusCode, 200)

	spans := tt2.WaitForSpans(t, 1)
	span := spans[0]

	assert.Equal(t, "global_service", span.Service)
}
