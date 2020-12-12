// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package fiber

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
)

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := fiber.New()
	router.Use(Middleware(WithServiceName("foobar")))
	router.Get("/user/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		_, ok := tracer.SpanFromContext(c.Context())
		assert.True(ok)

		return c.SendString(id)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	resp, err := router.Test(r, 100)

	assert.Equal(nil, err)
	assert.Equal(resp.StatusCode, 200)
}

func TestTrace200(t *testing.T) {
	assertDoRequest := func(assert *assert.Assertions, mt mocktracer.Tracer, router *fiber.App) {
		r := httptest.NewRequest("GET", "/user/123", nil)

		// do and verify the request
		resp, err := router.Test(r, 100)
		assert.Equal(nil, err)
		assert.Equal(resp.StatusCode, 200)

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
		assert.Equal("GET /user/{id}", span.Tag(ext.ResourceName))
		assert.Equal("200", span.Tag(ext.HTTPCode))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/user/123", span.Tag(ext.HTTPURL))
	}

	t.Run("response written", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := fiber.New()
		router.Use(Middleware(WithServiceName("foobar")))
		router.Get("/user/:id", func(c *fiber.Ctx) error {
			span, ok := tracer.SpanFromContext(c.Context())
			assert.True(ok)
			assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")
			id := c.Params("id")

			return c.SendString(id)
		})
		assertDoRequest(assert, mt, router)
	})

	t.Run("no response written", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := fiber.New()
		router.Use(Middleware(WithServiceName("foobar")))
		router.Get("/user/:id", func(c *fiber.Ctx) error {
			id := c.Params("id")
			span, ok := tracer.SpanFromContext(c.Context())
			assert.True(ok)
			assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")

			return c.SendString(id)
		})
		assertDoRequest(assert, mt, router)
	})
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// setup
	router := fiber.New()
	router.Use(Middleware(WithServiceName("foobar")))
	code := 500
	wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))

	// a handler with an error and make the requests
	router.Get("/err", func(c *fiber.Ctx) error {
		return c.Status(code).SendString(fmt.Sprintf("%d!", code))
	})
	r := httptest.NewRequest("GET", "/err", nil)

	response, err := router.Test(r, 100)
	assert.Equal(nil, err)
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
	assert.Equal(wantErr, span.Tag(ext.Error).(error).Error())
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)
	router := fiber.New()
	router.Get("/ping", func(c *fiber.Ctx) error {
		// Assert we don't have a span on the context.
		_, ok := tracer.SpanFromContext(c.Context())
		assert.False(ok)
		return c.SendString("ok")
	})
	r := httptest.NewRequest("GET", "/ping", nil)

	response, err := router.Test(r, 100)
	assert.Equal(nil, err)
	assert.Equal(response.StatusCode, 200)
}

func TestPropagation(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	r := httptest.NewRequest("GET", "/user/123", nil)

	pspan := tracer.StartSpan("test")
	tracer.Inject(pspan.Context(), tracer.HTTPHeadersCarrier(r.Header))

	router := fiber.New()
	router.Use(Middleware(WithServiceName("foobar")))
	router.Get("/user/:id", func(c *fiber.Ctx) error {
		span, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		assert.Equal(span.(mocktracer.Span).ParentID(), pspan.(mocktracer.Span).SpanID())
		return c.SendString("ok")
	})

	_, err := router.Test(r, 100)
	assert.Equal(nil, err)
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		router := fiber.New()
		router.Use(Middleware(opts...))
		router.Get("/user/:id", func(c *fiber.Ctx) error {
			_, ok := tracer.SpanFromContext(c.Context())
			assert.True(t, ok)
			return c.SendString("ok")
		})

		r := httptest.NewRequest("GET", "/user/123", nil)

		router.Test(r, 100)

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

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

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

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}
