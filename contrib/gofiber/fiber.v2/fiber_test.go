// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fiber

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
)

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := fiber.New()
	router.Use(Middleware(WithService("foobar")))
	router.Get("/user/:id", func(c *fiber.Ctx) error {
		return c.SendString(c.Params("id"))
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	resp, err := router.Test(r)
	assert.Equal(nil, err)
	defer resp.Body.Close()

	finishedSpans := mt.FinishedSpans()

	assert.Equal(1, len(finishedSpans))
	assert.Equal(resp.StatusCode, 200)
}

func TestTrace200(t *testing.T) {
	assertDoRequest := func(assert *assert.Assertions, mt mocktracer.Tracer, router *fiber.App) {
		r := httptest.NewRequest("GET", "/user/123", nil)

		// do and verify the request
		resp, err := router.Test(r)
		assert.Equal(nil, err)
		defer resp.Body.Close()
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
		assert.Equal("GET /user/:id", span.Tag(ext.ResourceName))
		assert.Equal("200", span.Tag(ext.HTTPCode))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/user/123", span.Tag(ext.HTTPURL))
		assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
		assert.Equal("gofiber/fiber.v2", span.Tag(ext.Component))
		assert.Equal(componentName, span.Integration())
		assert.Equal("/user/:id", span.Tag(ext.HTTPRoute))
	}

	t.Run("response", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := fiber.New()
		router.Use(Middleware(WithService("foobar")))
		router.Get("/user/:id", func(c *fiber.Ctx) error {
			return c.SendString(c.Params("id"))
		})

		assertDoRequest(assert, mt, router)
	})

	t.Run("no-response", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := fiber.New()
		router.Use(Middleware(WithService("foobar")))
		router.Get("/user/:id", func(c *fiber.Ctx) error {
			return c.SendString(c.Params("id"))
		})
		assertDoRequest(assert, mt, router)
	})
}

func TestStatusError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// setup
	router := fiber.New()
	router.Use(Middleware(WithService("foobar")))
	code := 500
	wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))

	// a handler with an error and make the requests
	router.Get("/err", func(c *fiber.Ctx) error {
		return c.Status(code).SendString(fmt.Sprintf("%d!", code))
	})
	r := httptest.NewRequest("GET", "/err", nil)

	response, err := router.Test(r)
	assert.Equal(nil, err)
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
	assert.Equal("/err", span.Tag(ext.HTTPRoute))
	assert.Equal(wantErr, span.Tag(ext.ErrorMsg))
}

func TestCustomError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := fiber.New()
	router.Use(Middleware(WithService("foobar")))

	router.Get("/err", func(c *fiber.Ctx) error {
		c.SendStatus(400)
		return fiber.ErrBadRequest
	})
	r := httptest.NewRequest("GET", "/err", nil)

	response, err := router.Test(r)
	assert.Equal(nil, err)
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 400)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("foobar", span.Tag(ext.ServiceName))
	assert.Equal("400", span.Tag(ext.HTTPCode))
	assert.Equal(fiber.ErrBadRequest.Error(), span.Tag(ext.ErrorMsg))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.Equal("gofiber/fiber.v2", span.Tag(ext.Component))
	assert.Equal(componentName, span.Integration())
	assert.Equal("/err", span.Tag(ext.HTTPRoute))
}

func TestUserContext(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// setup
	router := fiber.New()

	// define a custom context key
	type contextKey string
	const fooKey contextKey = "foo"

	// add a middleware that adds a value to the context
	router.Use(func(c *fiber.Ctx) error {
		ctx := context.WithValue(c.UserContext(), fooKey, "bar")
		c.SetUserContext(ctx)
		return c.Next()
	})

	// add the middleware
	router.Use(Middleware(WithService("foobar")))

	router.Get("/", func(c *fiber.Ctx) error {
		// check if not default empty context
		assert.NotEmpty(c.UserContext())

		// checks that the user context still has the information provided before using the middleware
		foo, ok := c.UserContext().Value(fooKey).(string)
		assert.True(ok)
		assert.Equal(foo, "bar")

		span, _ := tracer.StartSpanFromContext(c.UserContext(), "http.request")
		defer span.Finish()
		return c.SendString("test")
	})
	r := httptest.NewRequest("GET", "/", nil)

	resp, err := router.Test(r)
	assert.Nil(err)
	defer resp.Body.Close()

	// verify both middleware span and router span finished
	spans := mt.FinishedSpans()
	assert.Len(spans, 2)
	assert.Equal(spans[1].SpanID(), spans[0].ParentID())
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)
	router := fiber.New()
	router.Get("/ping", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	r := httptest.NewRequest("GET", "/ping", nil)

	response, err := router.Test(r)
	assert.Equal(nil, err)
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 200)
}

func TestPropagation(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	requestWithSpan := httptest.NewRequest("GET", "/span/exists/true", nil)
	pspan := tracer.StartSpan("test")
	tracer.Inject(pspan.Context(), tracer.HTTPHeadersCarrier(requestWithSpan.Header))

	requestWithoutSpan := httptest.NewRequest("GET", "/span/exists/false", nil)

	router := fiber.New()
	router.Use(Middleware(WithService("foobar")))
	router.Get("/span/exists/true", func(c *fiber.Ctx) error {
		s, _ := tracer.SpanFromContext(c.UserContext())
		assert.Equal(s.Context().TraceID() == pspan.Context().TraceID(), true)
		return c.SendString(c.Params("span exists"))
	})
	router.Get("/span/exists/false", func(c *fiber.Ctx) error {
		s, _ := tracer.SpanFromContext(c.UserContext())
		assert.Equal(s.Context().TraceID() == pspan.Context().TraceID(), false)
		return c.SendString(c.Params("span does not exist"))
	})

	resp, withoutErr := router.Test(requestWithoutSpan)
	assert.Equal(nil, withoutErr)
	defer resp.Body.Close()

	resp, withErr := router.Test(requestWithSpan)
	assert.Equal(nil, withErr)
	defer resp.Body.Close()
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		router := fiber.New()
		router.Use(Middleware(opts...))
		router.Get("/user/:id", func(c *fiber.Ctx) error {
			return c.SendString(c.Params("id"))
		})

		r := httptest.NewRequest("GET", "/user/123", nil)
		resp, err := router.Test(r)
		assert.Nil(t, err)
		defer resp.Body.Close()

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

func TestIgnoreRequest(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := fiber.New()
	router.Use(
		Middleware(
			WithIgnoreRequest(func(ctx *fiber.Ctx) bool {
				return ctx.Method() == "GET" && ctx.Path() == "/ignore"
			}),
		),
	)
	router.Get("/ignore", func(c *fiber.Ctx) error {
		return c.SendString("IAMALIVE")
	})

	r := httptest.NewRequest("GET", "/ignore", nil)

	// do and verify the request
	resp, err := router.Test(r)
	assert.Equal(nil, err)
	defer resp.Body.Close()
	assert.Equal(resp.StatusCode, 200)

	spans := mt.FinishedSpans()

	assert.Len(spans, 0)
}
