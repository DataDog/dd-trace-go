// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo"
	echotrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/labstack/echo"
)

type Integration struct {
	router   *echo.Echo
	numSpans int
	opts     []echotrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]echotrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "labstack/echo"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	i.router = echo.New()
	i.router.Use(echotrace.Middleware(i.opts...))

	i.router.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "test")
	})

	wantErr := errors.New("oh no")
	i.router.GET("/err", func(c echo.Context) error {
		err := wantErr
		c.Error(err)
		return err
	})

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	r := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	i.router.ServeHTTP(w, r)
	i.numSpans++

	r = httptest.NewRequest("GET", "/err", nil)
	w = httptest.NewRecorder()
	i.router.ServeHTTP(w, r)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, echotrace.WithServiceName(name))
}
