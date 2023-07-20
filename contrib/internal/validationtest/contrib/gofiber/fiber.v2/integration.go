// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fiber

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fibertrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gofiber/fiber.v2"
)

type Integration struct {
	router   *fiber.App
	numSpans int
	opts     []fibertrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]fibertrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "gofiber/fiber.v2"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	i.router = fiber.New()
	i.router.Use(fibertrace.Middleware(i.opts...))
	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	i.router.Get("/user/:id", func(c *fiber.Ctx) error {
		return c.SendString(c.Params("id"))
	})

	r := httptest.NewRequest("GET", "/user/2", nil)
	response, err := i.router.Test(r)
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, 200, response.StatusCode)
	i.numSpans++

	// a handler with an error and make the requests
	i.router.Get("/err", func(c *fiber.Ctx) error {
		return c.Status(500).SendString(fmt.Sprintf("%d!", 500))
	})
	r = httptest.NewRequest("GET", "/err", nil)

	response, err = i.router.Test(r)
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, 500, response.StatusCode)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, fibertrace.WithServiceName(name))
}
